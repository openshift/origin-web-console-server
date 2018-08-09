package assets

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

var varyHeaderRegexp = regexp.MustCompile("\\s*,\\s*")

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
	sniffDone bool
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.sniffDone {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.sniffDone = true
	}
	return w.Writer.Write(b)
}

// GzipHandler wraps a http.Handler to support transparent gzip encoding.
func GzipHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h.ServeHTTP(w, r)
			return
		}
		// Normalize the Accept-Encoding header for improved caching
		r.Header.Set("Accept-Encoding", "gzip")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		h.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

func SecurityHeadersHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-DNS-Prefetch-Control", "off")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.ServeHTTP(w, r)
	})
}

func generateEtag(r *http.Request, version string, varyHeaders []string) string {
	varyHeaderValues := ""
	for _, varyHeader := range varyHeaders {
		varyHeaderValues += r.Header.Get(varyHeader)
	}
	return fmt.Sprintf("W/\"%s_%s\"", version, hex.EncodeToString([]byte(varyHeaderValues)))
}

type LongestToShortest []string

func (s LongestToShortest) Len() int {
	return len(s)
}
func (s LongestToShortest) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s LongestToShortest) Less(i, j int) bool {
	return len(s[i]) > len(s[j])
}

// HTML5ModeHandler will serve any static assets we know about, all other paths
// are assumed to be HTML5 paths for the console application and index.html will
// be served.
// contextRoot must contain leading and trailing slashes, e.g. /console/
//
// subcontextMap is a map of keys (subcontexts, no leading or trailing slashes) to the asset path (no
// leading slash) to serve for that subcontext if a resource that does not exist is requested
func HTML5ModeHandler(contextRoot string, subcontextMap map[string]string, extensionScripts []string, extensionStylesheets []string, version string, h http.Handler, getAsset AssetFunc) (http.Handler, error) {
	subcontextData := map[string][]byte{}
	subcontexts := []string{}

	for subcontext, index := range subcontextMap {
		b, err := getAsset(index)
		if err != nil {
			return nil, err
		}
		base := path.Join(contextRoot, subcontext)
		// Make sure the base always ends in a trailing slash but don't end up with a double trailing slash
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		b = bytes.Replace(b, []byte(`<base href="/">`), []byte(fmt.Sprintf(`<base href="%s">`, base)), 1)

		// Inject extension scripts and stylesheets, but only for the console itself, which has an empty subcontext
		if len(subcontext) == 0 {
			if len(extensionScripts) > 0 {
				b = addExtensionScripts(b, extensionScripts)
			}
			if len(extensionStylesheets) > 0 {
				b = addExtensionStylesheets(b, extensionStylesheets)
			}
		}

		subcontextData[subcontext] = b
		subcontexts = append(subcontexts, subcontext)
	}

	// Sort by length, longest first
	sort.Sort(LongestToShortest(subcontexts))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlPath := strings.TrimPrefix(r.URL.Path, "/")
		if _, err := getAsset(urlPath); err != nil {
			// Find the index we want to serve instead
			for _, subcontext := range subcontexts {
				prefix := subcontext
				if subcontext != "" {
					prefix += "/"
				}
				if urlPath == subcontext || strings.HasPrefix(urlPath, prefix) {
					// This is dynamic content since the extensions can change the HTML. Don't cache.
					w.Header().Add("Cache-Control", "no-cache, no-store")
					w.Write(subcontextData[subcontext])
					return
				}
			}
		}

		// Only handle ETags for content that won't change. The index.html responses can have scripts and stylesheets injected.
		vary := w.Header().Get("Vary")
		varyHeaders := []string{}
		if vary != "" {
			varyHeaders = varyHeaderRegexp.Split(vary, -1)
		}
		etag := generateEtag(r, version, varyHeaders)

		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		// Clients must revalidate their cached copy every time.
		w.Header().Add("Cache-Control", "public, max-age=0, must-revalidate")
		w.Header().Add("ETag", etag)
		h.ServeHTTP(w, r)
	}), nil
}

// Add the extension scripts as the last scripts, just before the body closing tag.
func addExtensionScripts(content []byte, extensionScripts []string) []byte {
	var scriptTags bytes.Buffer
	for _, scriptURL := range extensionScripts {
		scriptTags.WriteString(fmt.Sprintf("<script src=\"%s\"></script>\n", html.EscapeString(scriptURL)))
	}

	replaceBefore := []byte("</body>")
	scriptTags.Write(replaceBefore)
	return bytes.Replace(content, replaceBefore, scriptTags.Bytes(), 1)
}

// Add the extension stylesheets as the last stylesheets, just before the head closing tag.
func addExtensionStylesheets(content []byte, extensionStylesheets []string) []byte {
	var styleTags bytes.Buffer
	for _, stylesheetURL := range extensionStylesheets {
		styleTags.WriteString(fmt.Sprintf("<link rel=\"stylesheet\" href=\"%s\">\n", html.EscapeString(stylesheetURL)))
	}

	replaceBefore := []byte("</head>")
	styleTags.Write(replaceBefore)
	return bytes.Replace(content, replaceBefore, styleTags.Bytes(), 1)
}

var versionTemplate = template.Must(template.New("webConsoleVersion").Parse(`
window.OPENSHIFT_VERSION = {
  console: "{{ .ConsoleVersion | js }}"
};
`))

type WebConsoleVersion struct {
	ConsoleVersion string
}

var extensionPropertiesTemplate = template.Must(template.New("webConsoleExtensionProperties").Parse(`
window.OPENSHIFT_EXTENSION_PROPERTIES = {
{{ range $i, $property := .ExtensionProperties }}{{ if $i }},{{ end }}
  "{{ $property.Key | js }}": "{{ $property.Value | js }}"{{ end }}
};
`))

type WebConsoleExtensionProperty struct {
	Key   string
	Value string
}

type WebConsoleExtensionProperties struct {
	ExtensionProperties []WebConsoleExtensionProperty
}

var configTemplate = template.Must(template.New("webConsoleConfig").Parse(`
window.OPENSHIFT_CONFIG = {
  apis: {
    hostPort: "{{ .APIGroupAddr | js}}",
    prefix: "{{ .APIGroupPrefix | js}}"
  },
  api: {
    openshift: {
      hostPort: "{{ .MasterAddr | js}}",
      prefix: "{{ .MasterPrefix | js}}"
    },
    k8s: {
      hostPort: "{{ .KubernetesAddr | js}}",
      prefix: "{{ .KubernetesPrefix | js}}"
    }
  },
  auth: {
  	oauth_authorize_uri: "{{ .OAuthAuthorizeURI | js}}",
	oauth_token_uri: "{{ .OAuthTokenURI | js}}",
  	oauth_redirect_base: "{{ .OAuthRedirectBase | js}}",
  	oauth_client_id: "{{ .OAuthClientID | js}}",
  	logout_uri: "{{ .LogoutURI | js}}"
  },
  {{ with .LimitRequestOverrides }}
  limitRequestOverrides: {
	limitCPUToMemoryPercent: {{ .LimitCPUToMemoryPercent }},
	cpuRequestToLimitPercent: {{ .CPURequestToLimitPercent }},
	memoryRequestToLimitPercent: {{ .MemoryRequestToLimitPercent }}
  },
  {{ end }}
  adminConsoleURL: "{{ .AdminConsoleURL }}",
  loggingURL: "{{ .LoggingURL | js}}",
  metricsURL: "{{ .MetricsURL | js}}",
  templateServiceBrokerEnabled: {{ .TemplateServiceBrokerEnabled }},
  inactivityTimeoutMinutes: {{ .InactivityTimeoutMinutes }},
  clusterResourceOverridesEnabled: {{ .ClusterResourceOverridesEnabled}}
};
`))

type WebConsoleConfig struct {
	// APIGroupAddr is the host:port the UI should call the API groups on. Scheme is derived from the scheme the UI is served on, so they must be the same.
	APIGroupAddr string
	// APIGroupPrefix is the API group context root
	APIGroupPrefix string
	// MasterAddr is the host:port the UI should call the master API on. Scheme is derived from the scheme the UI is served on, so they must be the same.
	MasterAddr string
	// MasterPrefix is the OpenShift API context root
	MasterPrefix string
	// KubernetesAddr is the host:port the UI should call the kubernetes API on. Scheme is derived from the scheme the UI is served on, so they must be the same.
	// TODO this is probably unneeded since everything goes through the openshift master's proxy
	KubernetesAddr string
	// KubernetesPrefix is the Kubernetes API context root
	KubernetesPrefix string
	// OAuthAuthorizeURI is the OAuth2 endpoint to use to request an API token. It must support request_type=token.
	OAuthAuthorizeURI string
	// OAuthTokenURI is the OAuth2 endpoint to use to request an API token. If set, the OAuthClientID must support a client_secret of "".
	OAuthTokenURI string
	// OAuthRedirectBase is the base URI of the web console. It must be a valid redirect_uri for the OAuthClientID
	OAuthRedirectBase string
	// OAuthClientID is the OAuth2 client_id to use to request an API token. It must be authorized to redirect to the web console URL.
	OAuthClientID string
	// LogoutURI is an optional (absolute) URI to redirect to after completing a logout. If not specified, the built-in logout page is shown.
	LogoutURI string
	// LoggingURL is the endpoint for logging (optional)
	LoggingURL string
	// MetricsURL is the endpoint for metrics (optional)
	MetricsURL string
	// LimitRequestOverrides contains the ratios for overriding request/limit on containers.
	// Applied in order:
	//   LimitCPUToMemoryPercent
	//   CPURequestToLimitPercent
	//   MemoryRequestToLimitPercent
	LimitRequestOverrides *ClusterResourceOverrideConfig
	// TemplateServiceBrokerEnabled tells the web console not to show normal templates to avoid duplicates items in the catalog for templates and template service broker service classes.
	TemplateServiceBrokerEnabled bool
	// InactivityTimeoutMinutes is the number of minutes of inactivity before you are automatically logged out of
	// the web console. If set to 0, inactivity timeout is disabled.
	InactivityTimeoutMinutes int64
	// ClusterResourceOverridesEnabled indicates that the cluster is configured for overcommit. When set to
	// true, the web console will hide the CPU request, CPU limit, and memory request fields in its editors
	// and skip validation on those fields. The memory limit field will still be displayed.
	ClusterResourceOverridesEnabled bool
	AdminConsoleURL                 string
}

// ClusterResourceOverrideConfig is the configuration for the ClusterResourceOverride
// admission controller which overrides user-provided container request/limit values.
type ClusterResourceOverrideConfig struct {
	metav1.TypeMeta
	// For each of the following, if a non-zero ratio is specified then the initial
	// value (if any) in the pod spec is overwritten according to the ratio.
	// LimitRange defaults are merged prior to the override.
	//
	// LimitCPUToMemoryPercent (if > 0) overrides the CPU limit to a ratio of the memory limit;
	// 100% overrides CPU to 1 core per 1GiB of RAM. This is done before overriding the CPU request.
	LimitCPUToMemoryPercent int64
	// CPURequestToLimitPercent (if > 0) overrides CPU request to a percentage of CPU limit
	CPURequestToLimitPercent int64
	// MemoryRequestToLimitPercent (if > 0) overrides memory request to a percentage of memory limit
	MemoryRequestToLimitPercent int64
}

func GeneratedConfigHandler(config WebConsoleConfig, version WebConsoleVersion, extensionProps WebConsoleExtensionProperties) (http.Handler, error) {
	var buffer bytes.Buffer
	if err := configTemplate.Execute(&buffer, config); err != nil {
		return nil, err
	}
	if err := versionTemplate.Execute(&buffer, version); err != nil {
		return nil, err
	}

	// We include the extension properties in config.js and not extensions.js because we
	// want them treated with the same caching behavior as the rest of the values in config.js
	if err := extensionPropertiesTemplate.Execute(&buffer, extensionProps); err != nil {
		return nil, err
	}
	content := buffer.Bytes()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "no-cache, no-store")
		w.Header().Add("Content-Type", "application/javascript")
		if _, err := w.Write(content); err != nil {
			utilruntime.HandleError(fmt.Errorf("Error serving Web Console config and version: %v", err))
		}
	}), nil
}
