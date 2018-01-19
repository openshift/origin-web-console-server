package cmd

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"path"
	"strconv"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/validation/field"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericapiserveroptions "k8s.io/apiserver/pkg/server/options"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilflag "k8s.io/apiserver/pkg/util/flag"

	"github.com/openshift/api/webconsole/v1"
	webconsoleapiutil "github.com/openshift/origin-web-console-server/pkg/apis/webconsole/util"
	localwebconsolev1 "github.com/openshift/origin-web-console-server/pkg/apis/webconsole/v1"
	"github.com/openshift/origin-web-console-server/pkg/apis/webconsole/validation"
	webconsoleserver "github.com/openshift/origin-web-console-server/pkg/assets/apiserver"
	"github.com/openshift/origin-web-console-server/pkg/origin-common/crypto"
	builtversion "github.com/openshift/origin-web-console-server/pkg/version"
)

type WebConsoleServerOptions struct {
	// we don't have any storage, so we shouldn't use the recommended options
	Audit    *genericoptions.AuditOptions
	Features *genericoptions.FeatureOptions

	StdOut io.Writer
	StdErr io.Writer

	WebConsoleConfig *v1.WebConsoleConfiguration
}

func NewWebConsoleServerOptions(out, errOut io.Writer) *WebConsoleServerOptions {
	o := &WebConsoleServerOptions{
		Audit:    genericoptions.NewAuditOptions(),
		Features: genericoptions.NewFeatureOptions(),

		StdOut: out,
		StdErr: errOut,
	}

	return o
}

func NewCommandStartWebConsoleServer(out, errOut io.Writer, stopCh <-chan struct{}) *cobra.Command {
	o := NewWebConsoleServerOptions(out, errOut)

	cmd := &cobra.Command{
		Use:   "origin-web-console",
		Short: "Launch a web console server",
		Long:  "Launch a web console server",
		RunE: func(c *cobra.Command, args []string) error {
			if err := o.Complete(c); err != nil {
				return err
			}
			if err := o.Validate(args); err != nil {
				return err
			}
			if err := o.RunWebConsoleServer(stopCh); err != nil {
				return err
			}
			return nil
		},
	}

	flags := cmd.Flags()
	o.Audit.AddFlags(flags)
	o.Features.AddFlags(flags)
	flags.String("config", "", "filename containing the WebConsoleConfig")

	return cmd
}

func (o WebConsoleServerOptions) Validate(args []string) error {
	if o.WebConsoleConfig == nil {
		return fmt.Errorf("missing config: specify --config")
	}

	validationResults := validation.ValidateWebConsoleConfiguration(o.WebConsoleConfig, field.NewPath("config"))
	if len(validationResults.Warnings) != 0 {
		for _, warning := range validationResults.Warnings {
			glog.Warningf("Warning: %v, web console start will continue.", warning)
		}
	}
	if len(validationResults.Errors) != 0 {
		return apierrors.NewInvalid(schema.GroupKind{Group: "webconsole.config.openshift.io", Kind: "AssetConfig"}, "", validationResults.Errors)
	}

	return nil
}

func (o *WebConsoleServerOptions) Complete(cmd *cobra.Command) error {
	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return err
	}
	if len(configFile) > 0 {
		content, err := ioutil.ReadFile(configFile)
		if err != nil {
			return err
		}
		configObj, err := runtime.Decode(configCodecs.UniversalDecoder(v1.SchemeGroupVersion, schema.GroupVersion{Group: "", Version: "v1"}), content)
		if err != nil {
			return err
		}
		config, ok := configObj.(*v1.WebConsoleConfiguration)
		if !ok {
			return fmt.Errorf("unexpected type: %T", configObj)
		}

		// TODO we have no codegeneration at the moment, so manually apply defaults
		localwebconsolev1.SetDefaults_WebConsoleConfiguration(config)
		webconsoleapiutil.ResolveWebConsoleConfigurationPaths(config, path.Dir(configFile))

		o.WebConsoleConfig = config
	}

	return nil
}

func (o WebConsoleServerOptions) Config() (*webconsoleserver.AssetServerConfig, error) {
	// all this work is ordinarily done by using the default flags to configure the listener options
	// instead of doing that, we're keeping the config inside of a single config file, so we're doing this
	// transformation here.
	bindHost, portString, err := net.SplitHostPort(o.WebConsoleConfig.ServingInfo.BindAddress)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return nil, err
	}
	sniCertKeys := []utilflag.NamedCertKey{}
	for _, nc := range o.WebConsoleConfig.ServingInfo.NamedCertificates {
		sniCert := utilflag.NamedCertKey{
			CertFile: nc.CertFile,
			KeyFile:  nc.KeyFile,
			Names:    nc.Names,
		}
		sniCertKeys = append(sniCertKeys, sniCert)
	}
	secureServingOptions := &genericapiserveroptions.SecureServingOptions{
		BindAddress: net.ParseIP(bindHost),
		BindPort:    port,
		BindNetwork: o.WebConsoleConfig.ServingInfo.BindNetwork,

		ServerCert: genericapiserveroptions.GeneratableKeyCert{
			CertKey: genericapiserveroptions.CertKey{
				CertFile: o.WebConsoleConfig.ServingInfo.CertFile,
				KeyFile:  o.WebConsoleConfig.ServingInfo.KeyFile,
			},
		},
		SNICertKeys: sniCertKeys,
	}

	serverConfig, err := webconsoleserver.NewAssetServerConfig(*o.WebConsoleConfig)
	if err != nil {
		return nil, err
	}

	if err := secureServingOptions.ApplyTo(&serverConfig.GenericConfig.Config); err != nil {
		return nil, err
	}
	if err := genericapiserveroptions.NewCoreAPIOptions().ApplyTo(serverConfig.GenericConfig); err != nil {
		return nil, err
	}
	if err := o.Audit.ApplyTo(&serverConfig.GenericConfig.Config); err != nil {
		return nil, err
	}
	if err := o.Features.ApplyTo(&serverConfig.GenericConfig.Config); err != nil {
		return nil, err
	}

	// all this work is ordinarily done by using the default flags to configure the listener options
	// instead of doing that, we're keeping the config inside of a single config file, so we're doing this
	// transformation here.
	serverConfig.GenericConfig.SecureServingInfo.MinTLSVersion = crypto.TLSVersionOrDie(o.WebConsoleConfig.ServingInfo.MinTLSVersion)
	serverConfig.GenericConfig.SecureServingInfo.CipherSuites = crypto.CipherSuitesOrDie(o.WebConsoleConfig.ServingInfo.CipherSuites)

	return serverConfig, nil
}

func (o WebConsoleServerOptions) RunWebConsoleServer(stopCh <-chan struct{}) error {
	config, err := o.Config()
	if err != nil {
		return err
	}

	completedConfig, err := config.Complete()
	if err != nil {
		return err
	}
	server, err := completedConfig.New(genericapiserver.EmptyDelegate)
	if err != nil {
		return err
	}
	glog.Infof("OpenShift Web Console Version: %s", builtversion.Get().String())
	return server.GenericAPIServer.PrepareRun().Run(stopCh)
}

// these are used to set up for reading the config
var (
	configScheme = runtime.NewScheme()
	configCodecs = serializer.NewCodecFactory(configScheme)
)

func init() {
	v1.AddToScheme(configScheme)
	configScheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "AssetConfig"}, &v1.WebConsoleConfiguration{})
}
