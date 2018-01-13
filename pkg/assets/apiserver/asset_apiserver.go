package apiserver

import (
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
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/features"
	"k8s.io/apiserver/pkg/server"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	genericmux "k8s.io/apiserver/pkg/server/mux"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/openshift/api/webconsole/v1"
	"github.com/openshift/origin-web-console-server/pkg/assets"
	"github.com/openshift/origin-web-console-server/pkg/assets/java"
	"github.com/openshift/origin-web-console-server/pkg/version"
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
	ExtraConfig   *ExtraConfig
}

type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

func NewAssetServerConfig(assetConfig v1.WebConsoleConfiguration) (*AssetServerConfig, error) {
	publicURL, err := url.Parse(assetConfig.PublicURL)
	if err != nil {
		return nil, err
	}

	genericConfig := genericapiserver.NewConfig(codecs)
	genericConfig.EnableDiscovery = false
	genericConfig.BuildHandlerChainFunc = buildHandlerChainForAssets(publicURL.Path)

	return &AssetServerConfig{
		GenericConfig: &genericapiserver.RecommendedConfig{Config: *genericConfig},
		ExtraConfig: ExtraConfig{
			Options:   assetConfig,
			PublicURL: *publicURL,
		},
	}, nil
}

// Complete fills in any fields not set that are required to have valid data. It's mutating the receiver.
func (c *AssetServerConfig) Complete() completedConfig {
	cfg := completedConfig{
		c.GenericConfig.Complete(),
		&c.ExtraConfig,
	}

	return cfg
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
	masterURL, err := url.Parse(c.ExtraConfig.Options.MasterPublicURL)
	if err != nil {
		return err
	}

	// Generated web console config and server version
	config := assets.WebConsoleConfig{
		APIGroupAddr:      masterURL.Host,
		APIGroupPrefix:    server.APIGroupPrefix,
		MasterAddr:        masterURL.Host,
		MasterPrefix:      "/oapi",
		KubernetesAddr:    masterURL.Host,
		KubernetesPrefix:  server.DefaultLegacyAPIPrefix,
		OAuthAuthorizeURI: openShiftOAuthAuthorizeURL(masterURL.String()),
		OAuthTokenURI:     openShiftOAuthTokenURL(masterURL.String()),
		OAuthRedirectBase: c.ExtraConfig.Options.PublicURL,
		OAuthClientID:     OpenShiftWebConsoleClientID,
		LogoutURI:         c.ExtraConfig.Options.LogoutURL,
		LoggingURL:        c.ExtraConfig.Options.LoggingPublicURL,
		MetricsURL:        c.ExtraConfig.Options.MetricsPublicURL,
	}

	// TODO this looks incorrect.  I'm guessing that we need to contact the master and locate the actual versions
	oVersionInfo := version.Get()
	versionInfo := assets.WebConsoleVersion{
		KubernetesVersion: oVersionInfo.GitVersion,
		OpenShiftVersion:  oVersionInfo.GitVersion,
	}

	extensionProps := assets.WebConsoleExtensionProperties{
		ExtensionProperties: extensionPropertyArray(c.ExtraConfig.Options.ExtensionProperties),
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
	handler, err = assets.HTML5ModeHandler(c.ExtraConfig.PublicURL.Path, subcontextMap, c.ExtraConfig.Options.ExtensionScripts, c.ExtraConfig.Options.ExtensionStylesheets, handler, assetFunc)
	if err != nil {
		return nil, err
	}

	// Cache control should happen after all Vary headers are added, but before
	// any asset related routing (HTML5ModeHandler and FileServer)
	handler = assets.CacheControlHandler(version.Get().GitCommit, handler)

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
