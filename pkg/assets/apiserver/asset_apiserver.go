package apiserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/elazarl/go-bindata-assetfs"

	"k8s.io/apimachinery/pkg/apimachinery/announced"
	"k8s.io/apimachinery/pkg/apimachinery/registered"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/version"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/server"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	genericmux "k8s.io/apiserver/pkg/server/mux"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	restclient "k8s.io/client-go/rest"

	"github.com/openshift/api/webconsole/v1"
	"github.com/openshift/origin-web-console-server/pkg/assets"
	"github.com/openshift/origin-web-console-server/pkg/assets/java"
	builtversion "github.com/openshift/origin-web-console-server/pkg/version"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
)

var (
	groupFactoryRegistry = make(announced.APIGroupFactoryRegistry)
	registry             = registered.NewOrDie("")
	scheme               = runtime.NewScheme()
	codecs               = serializer.NewCodecFactory(scheme)

	// if you modify this, make sure you update the crEncoder
	unversionedVersion = schema.GroupVersion{Group: "", Version: "v1"}
	unversionedTypes   = []runtime.Object{
		&metav1.Status{},
		&metav1.WatchEvent{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	}
)

func init() {
	// we need to add the options to empty v1
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Group: "", Version: "v1"})
	scheme.AddUnversionedTypes(unversionedVersion, unversionedTypes...)
}

const (
	OpenShiftWebConsoleClientID = "openshift-web-console"
)

type ExtraConfig struct {
	Options   v1.WebConsoleConfiguration
	PublicURL url.URL

	KubeVersion                  string
	OpenShiftVersion             string
	TemplateServiceBrokerEnabled *bool
}

type AssetServerConfig struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// AssetServer serves non-API endpoints for openshift.
type AssetServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer

	PublicURL url.URL
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig

	// ClientConfig holds the kubernetes client configuration.
	// This value is set by RecommendedOptions.CoreAPI.ApplyTo called by RecommendedOptions.ApplyTo.
	// By default in-cluster client config is used.
	ClientConfig *restclient.Config
	ExtraConfig  *ExtraConfig
}

type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

func NewAssetServerConfig(config v1.WebConsoleConfiguration) (*AssetServerConfig, error) {
	publicURL, err := url.Parse(config.ClusterInfo.ConsolePublicURL)
	if err != nil {
		return nil, err
	}

	genericConfig := genericapiserver.NewConfig(codecs)
	genericConfig.EnableDiscovery = false
	genericConfig.EnableMetrics = true
	genericConfig.BuildHandlerChainFunc = buildHandlerChainForAssets(publicURL.Path)

	return &AssetServerConfig{
		GenericConfig: &genericapiserver.RecommendedConfig{Config: *genericConfig},
		ExtraConfig: ExtraConfig{
			Options:   config,
			PublicURL: *publicURL,
		},
	}, nil
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (c *AssetServerConfig) Complete() (completedConfig, error) {
	cfg := completedConfig{
		c.GenericConfig.Complete(),
		c.GenericConfig.ClientConfig,
		&c.ExtraConfig,
	}

	// In order to build the asset server, we need information that we can only retrieve from the master.
	// We do this once during construction at the moment, but a clever person could set up a watch or poll technique
	// to dynamically reload these bits of config without having the pod restart.
	restClient, err := kubernetes.NewForConfig(c.GenericConfig.ClientConfig)
	if err != nil {
		return completedConfig{}, err
	}
	if len(cfg.ExtraConfig.KubeVersion) == 0 {
		resultBytes, err := restClient.RESTClient().Get().AbsPath("/version").Do().Raw()
		if err != nil {
			return completedConfig{}, err
		}
		kubeVersion := &version.Info{}
		if err := json.Unmarshal(resultBytes, kubeVersion); err != nil {
			return completedConfig{}, err
		}
		cfg.ExtraConfig.KubeVersion = kubeVersion.GitVersion
	}
	if len(cfg.ExtraConfig.OpenShiftVersion) == 0 {
		resultBytes, err := restClient.RESTClient().Get().AbsPath("/version/openshift").Do().Raw()
		if err != nil {
			return completedConfig{}, err
		}
		openshiftVersion := &version.Info{}
		if err := json.Unmarshal(resultBytes, openshiftVersion); err != nil {
			return completedConfig{}, err
		}
		cfg.ExtraConfig.OpenShiftVersion = openshiftVersion.GitVersion
	}
	if cfg.ExtraConfig.TemplateServiceBrokerEnabled == nil {
		enabled := false
		_, err := restClient.RESTClient().Get().AbsPath("/apis/servicecatalog.k8s.io/v1beta1/clusterservicebrokers/template-service-broker").Do().Raw()
		if err == nil {
			enabled = true
		} else if errors.IsNotFound(err) {
			enabled = false
		} else {
			return completedConfig{}, err
		}

		cfg.ExtraConfig.TemplateServiceBrokerEnabled = &enabled
	}

	return cfg, nil
}

func (c completedConfig) New(delegationTarget genericapiserver.DelegationTarget) (*AssetServer, error) {
	genericServer, err := c.GenericConfig.New("origin-web-console-server", delegationTarget)
	if err != nil {
		return nil, err
	}

	s := &AssetServer{
		GenericAPIServer: genericServer,
		PublicURL:        c.ExtraConfig.PublicURL,
	}

	if err := c.addAssets(s.GenericAPIServer.Handler.NonGoRestfulMux); err != nil {
		return nil, err
	}
	if err := c.addWebConsoleConfig(s.GenericAPIServer.Handler.NonGoRestfulMux); err != nil {
		return nil, err
	}

	return s, nil
}

// buildHandlerChainForAssets is the handling chain used to protect the asset server.  With no secret information to protect
// the chain is very short.
func buildHandlerChainForAssets(consoleRedirectPath string) func(startingHandler http.Handler, c *genericapiserver.Config) http.Handler {
	return func(startingHandler http.Handler, c *genericapiserver.Config) http.Handler {
		handler := WithAssetServerRedirect(startingHandler, consoleRedirectPath)
		handler = genericfilters.WithMaxInFlightLimit(handler, c.MaxRequestsInFlight, c.MaxMutatingRequestsInFlight, c.RequestContextMapper, c.LongRunningFunc)
		if utilfeature.DefaultFeatureGate.Enabled(features.AdvancedAuditing) {
			handler = genericapifilters.WithAudit(handler, c.RequestContextMapper, c.AuditBackend, c.AuditPolicyChecker, c.LongRunningFunc)
		}
		handler = genericfilters.WithCORS(handler, c.CorsAllowedOriginList, nil, nil, nil, "true")
		handler = genericfilters.WithTimeoutForNonLongRunningRequests(handler, c.RequestContextMapper, c.LongRunningFunc, c.RequestTimeout)
		handler = genericapifilters.WithRequestInfo(handler, genericapiserver.NewRequestInfoResolver(c), c.RequestContextMapper)
		handler = apirequest.WithRequestContext(handler, c.RequestContextMapper)
		handler = genericfilters.WithPanicRecovery(handler)

		return handler
	}
}

func (c completedConfig) addAssets(serverMux *genericmux.PathRecorderMux) error {
	assetHandler, err := c.buildAssetHandler()
	if err != nil {
		return err
	}

	serverMux.UnlistedHandlePrefix(c.ExtraConfig.PublicURL.Path, http.StripPrefix(c.ExtraConfig.PublicURL.Path, assetHandler))
	serverMux.UnlistedHandle(c.ExtraConfig.PublicURL.Path[0:len(c.ExtraConfig.PublicURL.Path)-1], http.RedirectHandler(c.ExtraConfig.PublicURL.Path, http.StatusMovedPermanently))
	return nil
}

func (c *completedConfig) addWebConsoleConfig(serverMux *genericmux.PathRecorderMux) error {
	masterURL, err := url.Parse(c.ExtraConfig.Options.ClusterInfo.MasterPublicURL)
	if err != nil {
		return err
	}

	// Generated web console config and server version
	config := assets.WebConsoleConfig{
		APIGroupAddr:                    masterURL.Host,
		APIGroupPrefix:                  server.APIGroupPrefix,
		MasterAddr:                      masterURL.Host,
		MasterPrefix:                    "/oapi",
		KubernetesAddr:                  masterURL.Host,
		KubernetesPrefix:                server.DefaultLegacyAPIPrefix,
		OAuthAuthorizeURI:               openShiftOAuthAuthorizeURL(masterURL.String()),
		OAuthTokenURI:                   openShiftOAuthTokenURL(masterURL.String()),
		OAuthRedirectBase:               c.ExtraConfig.Options.ClusterInfo.ConsolePublicURL,
		OAuthClientID:                   OpenShiftWebConsoleClientID,
		LogoutURI:                       c.ExtraConfig.Options.ClusterInfo.LogoutPublicURL,
		LoggingURL:                      c.ExtraConfig.Options.ClusterInfo.LoggingPublicURL,
		MetricsURL:                      c.ExtraConfig.Options.ClusterInfo.MetricsPublicURL,
		InactivityTimeoutMinutes:        c.ExtraConfig.Options.Features.InactivityTimeoutMinutes,
		ClusterResourceOverridesEnabled: c.ExtraConfig.Options.Features.ClusterResourceOverridesEnabled,
	}

	if c.ExtraConfig.TemplateServiceBrokerEnabled != nil {
		config.TemplateServiceBrokerEnabled = *c.ExtraConfig.TemplateServiceBrokerEnabled
	}

	versionInfo := assets.WebConsoleVersion{
		KubernetesVersion: c.ExtraConfig.KubeVersion,
		OpenShiftVersion:  c.ExtraConfig.OpenShiftVersion,
		ConsoleVersion:    builtversion.Get().String(),
	}

	extensionProps := assets.WebConsoleExtensionProperties{
		ExtensionProperties: extensionPropertyArray(c.ExtraConfig.Options.Extensions.Properties),
	}
	configPath := path.Join(c.ExtraConfig.PublicURL.Path, "config.js")
	configHandler, err := assets.GeneratedConfigHandler(config, versionInfo, extensionProps)
	configHandler = assets.SecurityHeadersHandler(configHandler)
	if err != nil {
		return err
	}
	serverMux.UnlistedHandle(configPath, assets.GzipHandler(configHandler))

	return nil
}

func (c completedConfig) buildAssetHandler() (http.Handler, error) {
	assets.RegisterMimeTypes()

	assetFunc := assets.JoinAssetFuncs(assets.Asset, java.Asset)
	assetDirFunc := assets.JoinAssetDirFuncs(assets.AssetDir, java.AssetDir)

	handler := http.FileServer(&assetfs.AssetFS{Asset: assetFunc, AssetDir: assetDirFunc, Prefix: ""})

	// Map of context roots (no leading or trailing slash) to the asset path to serve for requests to a missing asset
	subcontextMap := map[string]string{
		"":     "index.html",
		"java": "java/index.html",
	}

	var err error
	version := builtversion.Get().GitCommit

	// This handler must be in the chain after GzipHandler so that GzipHandler can add the Vary
	// response header first. ETags should be different when the response uses gzip.
	handler, err = assets.HTML5ModeHandler(
		c.ExtraConfig.PublicURL.Path,
		subcontextMap,
		c.ExtraConfig.Options.Extensions.ScriptURLs,
		c.ExtraConfig.Options.Extensions.StylesheetURLs,
		version,
		handler,
		assetFunc,
	)
	if err != nil {
		return nil, err
	}

	handler = assets.SecurityHeadersHandler(handler)

	// Gzip first so that inner handlers can react to the addition of the Vary header
	handler = assets.GzipHandler(handler)

	return handler, nil
}

// Have to convert to arrays because go templates are limited and we need to be able to know
// if we are on the last index for trailing commas in JSON
func extensionPropertyArray(extensionProperties map[string]string) []assets.WebConsoleExtensionProperty {
	extensionPropsArray := []assets.WebConsoleExtensionProperty{}
	for key, value := range extensionProperties {
		extensionPropsArray = append(extensionPropsArray, assets.WebConsoleExtensionProperty{
			Key:   key,
			Value: value,
		})
	}
	return extensionPropsArray
}

// If we know the location of the asset server, redirect to it when / is requested
// and the Accept header supports text/html
// This should *only* be hit with browser, so just unconditionally redirect
func WithAssetServerRedirect(handler http.Handler, assetPublicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" {
			http.Redirect(w, req, assetPublicURL, http.StatusFound)
		}
		// Dispatch to the next handler
		handler.ServeHTTP(w, req)
	})
}

const (
	OpenShiftOAuthAPIPrefix = "/oauth"
	AuthorizePath           = "/authorize"
	TokenPath               = "/token"
)

func openShiftOAuthAuthorizeURL(masterAddr string) string {
	return openShiftOAuthURL(masterAddr, AuthorizePath)
}
func openShiftOAuthTokenURL(masterAddr string) string {
	return openShiftOAuthURL(masterAddr, TokenPath)
}
func openShiftOAuthURL(masterAddr, oauthEndpoint string) string {
	return strings.TrimRight(masterAddr, "/") + path.Join(OpenShiftOAuthAPIPrefix, oauthEndpoint)
}
