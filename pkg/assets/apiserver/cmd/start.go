package cmd

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"strconv"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericapiserveroptions "k8s.io/apiserver/pkg/server/options"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	utilflag "k8s.io/apiserver/pkg/util/flag"
	"k8s.io/kubernetes/pkg/kubectl/cmd/util"

	webconsoleserver "github.com/openshift/origin-web-console-server/pkg/assets/apiserver"
	configapi "github.com/openshift/origin/pkg/cmd/server/api"
	configapiinstall "github.com/openshift/origin/pkg/cmd/server/api/install"
	configapivalidation "github.com/openshift/origin/pkg/cmd/server/api/validation"
	"github.com/openshift/origin/pkg/cmd/server/crypto"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

type WebConsoleServerOptions struct {
	// we don't have any storage, so we shouldn't use the recommended options
	Audit    *genericoptions.AuditOptions
	Features *genericoptions.FeatureOptions

	StdOut io.Writer
	StdErr io.Writer

	WebConsoleConfig *configapi.AssetConfig
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

	validationResults := configapivalidation.ValidateAssetConfig(o.WebConsoleConfig, field.NewPath("config"))
	if len(validationResults.Warnings) != 0 {
		for _, warning := range validationResults.Warnings {
			glog.Warningf("Warning: %v, web console start will continue.", warning)
		}
	}
	if len(validationResults.Errors) != 0 {
		return apierrors.NewInvalid(configapi.Kind("AssetConfig"), "", validationResults.Errors)
	}

	return nil
}

func (o *WebConsoleServerOptions) Complete(cmd *cobra.Command) error {
	configFile := util.GetFlagString(cmd, "config")
	if len(configFile) > 0 {
		content, err := ioutil.ReadFile(configFile)
		if err != nil {
			return err
		}
		configObj, err := runtime.Decode(configCodecs.UniversalDecoder(), content)
		if err != nil {
			return err
		}
		config, ok := configObj.(*configapi.AssetConfig)
		if !ok {
			return fmt.Errorf("unexpected type: %T", configObj)
		}
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
				CertFile: o.WebConsoleConfig.ServingInfo.ServerCert.CertFile,
				KeyFile:  o.WebConsoleConfig.ServingInfo.ServerCert.KeyFile,
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

	server, err := config.Complete().New(genericapiserver.EmptyDelegate)
	if err != nil {
		return err
	}
	return server.GenericAPIServer.PrepareRun().Run(stopCh)
}

// these are used to set up for reading the config
var (
	configScheme = runtime.NewScheme()
	configCodecs = serializer.NewCodecFactory(configScheme)
)

func init() {
	configapiinstall.AddToScheme(configScheme)
}
