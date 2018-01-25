package assets

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func stubHandler(response string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(response) > 0 {
			w.Write([]byte(response))
		}
	})
}

func TestWebConsoleConfigTemplate(t *testing.T) {
	handler, err := GeneratedConfigHandler(WebConsoleConfig{}, WebConsoleVersion{}, WebConsoleExtensionProperties{})
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, &http.Request{Method: "GET"})
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	response := writer.Body.String()
	if !strings.Contains(response, "OPENSHIFT_CONFIG") {
		t.Errorf("body does not have OPENSHIFT_CONFIG:\n%s", response)
	}
	if strings.Contains(response, "limitRequestOverrides") {
		t.Errorf("LimitRequestOverrides should be omitted from the body:\n%s", response)
	}
}

func TestWithoutGzip(t *testing.T) {
	const resp = "hello"
	handler := GzipHandler(stubHandler(resp))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, &http.Request{Method: "GET"})
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	if l := writer.Body.Len(); l != len(resp) {
		t.Fatalf("invalid body length, got %d", l)
	}
	vary := writer.Header()["Vary"]
	if !reflect.DeepEqual(vary, []string{"Accept-Encoding"}) {
		t.Fatalf("expected a Vary header with value Accept-Encoding, got %v", vary)
	}
}

func TestWithoutGzipWithMultipleVaryHeaders(t *testing.T) {
	const resp = "hello"
	handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Foo")
		w.Write([]byte(resp))
	}))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, &http.Request{Method: "GET"})
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	if l := writer.Body.Len(); l != len(resp) {
		t.Fatalf("invalid body length, got %d", l)
	}
	vary := writer.Header()["Vary"]
	if !reflect.DeepEqual(vary, []string{"Accept-Encoding", "Foo"}) {
		t.Fatalf("invalid Vary headers, got %#v", vary)
	}
}

func TestWithGzip(t *testing.T) {
	handler := GzipHandler(stubHandler("hello"))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, &http.Request{
		Method: "GET",
		Header: http.Header{
			"Accept-Encoding": []string{"gzip"},
		},
	})
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	if l := writer.Body.Len(); l != 29 {
		t.Fatalf("invalid body length, got %d", l)
	}
	vary := writer.Header()["Vary"]
	if !reflect.DeepEqual(vary, []string{"Accept-Encoding"}) {
		t.Fatalf("invalid Vary headers, got %#v", vary)
	}
}

func TestWithGzipAndMultipleVaryHeader(t *testing.T) {
	handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Foo")
		w.Write([]byte("hello"))
	}))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, &http.Request{
		Method: "GET",
		Header: http.Header{
			"Accept-Encoding": []string{"gzip"},
		},
	})
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	if l := writer.Body.Len(); l != 29 {
		t.Fatalf("invalid body length, got %d", l)
	}
	vary := writer.Header()["Vary"]
	if !reflect.DeepEqual(vary, []string{"Accept-Encoding", "Foo"}) {
		t.Fatalf("invalid Vary headers, got %#v", vary)
	}
}

func TestWithGzipReal(t *testing.T) {
	const raw = "hello"
	handler := GzipHandler(stubHandler(raw))
	server := httptest.NewServer(handler)
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed http request: %s", err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if string(body) != raw {
		t.Fatalf(`did not find expected "%s" but got "%s" instead`, raw, string(body))
	}
	vary := resp.Header["Vary"]
	if !reflect.DeepEqual(vary, []string{"Accept-Encoding"}) {
		t.Fatalf("invalid Vary headers, got %#v", vary)
	}
}

func TestWithGzipRealAndMultipleVaryHeaders(t *testing.T) {
	const raw = "hello"
	handler := GzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Foo")
		w.Write([]byte(raw))
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("failed http request: %s", err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if string(body) != raw {
		t.Fatalf(`did not find expected "%s" but got "%s" instead`, raw, string(body))
	}
	vary := resp.Header["Vary"]
	if !reflect.DeepEqual(vary, []string{"Accept-Encoding", "Foo"}) {
		t.Fatalf("invalid Vary headers, got %#v", vary)
	}
}

func TestWithGzipDoubleWrite(t *testing.T) {
	handler := GzipHandler(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(bytes.Repeat([]byte("foo"), 1000))
			w.Write(bytes.Repeat([]byte("bar"), 1000))
		}))
	writer := httptest.NewRecorder()
	handler.ServeHTTP(writer, &http.Request{
		Method: "GET",
		Header: http.Header{
			"Accept-Encoding": []string{"gzip"},
		},
	})
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	if l := writer.Body.Len(); l != 54 {
		t.Fatalf("invalid body length, got %d", l)
	}
}

func TestGenerateEtag(t *testing.T) {
	etag := generateEtag(
		&http.Request{
			Method: "GET",
			Header: http.Header{
				"Foo": []string{"123"},
				"Bar": []string{"456"},
				"Baz": []string{"789"},
			},
		},
		"1234",
		[]string{"Foo", "Bar"},
	)
	expected := "W/\"1234_313233343536\""
	if etag != expected {
		t.Fatalf("Expected %s, got %s", expected, etag)
	}
}

const (
	// existingAssetURL is the URL of an actual asset in our bindata
	existingAssetURL = "https://example.com/scripts/vendor.js"
	// indexURL is a URL that isn't in bindata, which should return index.html
	indexURL = "https://example.com/projects/my-project"
)

func makeHTML5ModeHandler() (http.Handler, error) {
	subcontextMap := map[string]string{
		"": "index.html",
	}
	return HTML5ModeHandler(
		"/console/",
		subcontextMap,
		[]string{},
		[]string{},
		"1234",
		stubHandler(""),
		Asset,
	)
}

func TestCacheWithoutEtag(t *testing.T) {
	handler, err := makeHTML5ModeHandler()
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	writer := httptest.NewRecorder()
	request, err := http.NewRequest("GET", existingAssetURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	handler.ServeHTTP(writer, request)
	if writer.Header().Get("ETag") == "" {
		t.Fatal("ETag header was not set")
	}
}

func TestCacheWithInvalidEtag(t *testing.T) {
	handler, err := makeHTML5ModeHandler()
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	writer := httptest.NewRecorder()
	request, err := http.NewRequest("GET", existingAssetURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	request.Header.Set("If-None-Match", "123")
	handler.ServeHTTP(writer, request)
	if writer.Code == 304 {
		t.Fatal("Set status to Not Modified (304) on an invalid etag")
	}
}

func TestCacheWithValidEtag(t *testing.T) {
	handler, err := makeHTML5ModeHandler()
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	writer := httptest.NewRecorder()
	request, err := http.NewRequest("GET", existingAssetURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	etag := generateEtag(request, "1234", []string{})
	request.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(writer, request)
	if writer.Code != 304 {
		t.Fatalf("Expected status to be Not Modified (304), got %d.  Expected etag was %s, actual was %s", writer.Code, etag, writer.Header().Get("ETag"))
	}
}

func TestIndexHtml(t *testing.T) {
	handler, err := makeHTML5ModeHandler()
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	writer := httptest.NewRecorder()
	// Request a URL that does not exist, which serves index.html
	request, err := http.NewRequest("GET", indexURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	handler.ServeHTTP(writer, request)
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	if writer.Code != 200 {
		t.Fatalf("Expected status to be OK (200), got %d", writer.Code)
	}
}

func TestIndexHtmlNotCached(t *testing.T) {
	handler, err := makeHTML5ModeHandler()
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	writer := httptest.NewRecorder()
	// Request a URL that does not exist, which serves index.html
	request, err := http.NewRequest("GET", indexURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	etag := generateEtag(request, "1234", []string{})
	request.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(writer, request)
	if writer.Code == 304 {
		t.Fatalf("Set status to Not Modified (304) on a page that should not be cached")
	}
}

func TestETagGzip(t *testing.T) {
	handler, err := makeHTML5ModeHandler()
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}
	handler = GzipHandler(handler)

	// Make a request without an Accept-Encoding header
	writer := httptest.NewRecorder()
	request, err := http.NewRequest("GET", existingAssetURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	handler.ServeHTTP(writer, request)
	noVaryETag := writer.Header().Get("ETag")

	// Make a request with an Accept-Encoding header
	writer = httptest.NewRecorder()
	request, err = http.NewRequest("GET", existingAssetURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	request.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(writer, request)
	varyETag := writer.Header().Get("ETag")

	// Check that the ETags are different
	if varyETag == noVaryETag {
		t.Fatalf("expected different ETag when gzip enabled, got %s for both requests", varyETag)
	}
}

func TestExtensions(t *testing.T) {
	subcontextMap := map[string]string{
		"": "index.html",
	}
	scripts := []string{
		"https://extensions.example.com/scripts/menus.js",
		"https://extensions.example.com/scripts/nav.js",
	}
	stylesheets := []string{
		"https://extensions.example.com/styles/logo.css",
		"https://extensions.example.com/styles/styles.css",
	}
	handler, err := HTML5ModeHandler(
		"/console/",
		subcontextMap,
		scripts,
		stylesheets,
		"1234",
		stubHandler(""),
		Asset,
	)
	if err != nil {
		t.Fatalf("expected a handler, got error %v", err)
	}

	// Request index.html
	writer := httptest.NewRecorder()
	request, err := http.NewRequest("GET", indexURL, nil)
	if err != nil {
		t.Fatalf("expected a request, got error %v", err)
	}
	handler.ServeHTTP(writer, request)
	if writer.Code != 200 {
		t.Fatalf("Expected status to be OK (200), got %d", writer.Code)
	}
	if writer.Body == nil {
		t.Fatal("expected a body")
	}
	html := writer.Body.String()

	// Test that each script is in the body.
	for _, script := range scripts {
		if !strings.Contains(html, script) {
			t.Errorf("body does not have script %s:\n%s", script, html)
		}
	}

	// Test that each stylesheet is in the body.
	for _, stylesheet := range stylesheets {
		if !strings.Contains(html, stylesheet) {
			t.Errorf("body does not have stylesheet %s:\n%s", stylesheet, html)
		}
	}
}
