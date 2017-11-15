OpenShift Web Console Server
============================

The API server for the [OpenShift web console](https://github.com/openshift/origin-web-console), part of the
[OpenShift](https://github.com/openshift/origin) application platform.

[![Build Status](https://travis-ci.org/openshift/origin-web-console-server.svg?branch=master)](https://travis-ci.org/openshift/origin-web-console-server)

The web console server runs as a pod on the platform. The OpenShift master
proxies requests from the web console context root, typically `/console/`, to
the server running in the pod. The pod then serves the static HTML, JavaScript,
and CSS files that make up the console.

The web console assets themselves are developed in the
[origin-web-console](https://github.com/openshift/origin-web-console)
repository. They are included in the web console server binary using
[go-bindata](https://github.com/jteeuwen/go-bindata).

**Note**: This is a work in progress. Before OpenShift 3.8, the web console
asset server was part of master itself and did not run in a pod.

Building the Image
------------------

You will need to install [Go](https://golang.org/) 1.8 and Docker to build the image.

On Linux, build the image with

```
$ make build-image
```

On other platforms, you'll need to cross-compile a Linux binary with the command

```
$ GOOS=linux GOARCH=amd64 make build-image
```
