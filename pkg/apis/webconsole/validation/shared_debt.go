package validation

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/openshift/api/webconsole/v1"
	"github.com/openshift/origin/pkg/cmd/server/crypto"
)

// TODO this entire file needs to collapse with other config code

type ValidationResults struct {
	Warnings field.ErrorList
	Errors   field.ErrorList
}

func (r *ValidationResults) Append(additionalResults ValidationResults) {
	r.AddErrors(additionalResults.Errors...)
	r.AddWarnings(additionalResults.Warnings...)
}

func (r *ValidationResults) AddErrors(errors ...*field.Error) {
	if len(errors) == 0 {
		return
	}
	r.Errors = append(r.Errors, errors...)
}

func (r *ValidationResults) AddWarnings(warnings ...*field.Error) {
	if len(warnings) == 0 {
		return
	}
	r.Warnings = append(r.Warnings, warnings...)
}

func ValidateHTTPServingInfo(info v1.HTTPServingInfo, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	validationResults.Append(ValidateServingInfo(info.ServingInfo, true, fldPath))

	if info.MaxRequestsInFlight < 0 {
		validationResults.AddErrors(field.Invalid(fldPath.Child("maxRequestsInFlight"), info.MaxRequestsInFlight, "must be zero (no limit) or greater"))
	}

	if info.RequestTimeoutSeconds < -1 {
		validationResults.AddErrors(field.Invalid(fldPath.Child("requestTimeoutSeconds"), info.RequestTimeoutSeconds, "must be -1 (no timeout), 0 (default timeout), or greater"))
	}

	return validationResults
}

func ValidateServingInfo(info v1.ServingInfo, certificatesRequired bool, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	validationResults.AddErrors(ValidateHostPort(info.BindAddress, fldPath.Child("bindAddress"))...)
	validationResults.AddErrors(ValidateCertInfo(info.CertInfo, certificatesRequired, fldPath)...)

	if len(info.NamedCertificates) > 0 && len(info.CertFile) == 0 {
		validationResults.AddErrors(field.Invalid(fldPath.Child("namedCertificates"), "", "a default certificate and key is required in certFile/keyFile in order to use namedCertificates"))
	}

	validationResults.Append(ValidateNamedCertificates(fldPath.Child("namedCertificates"), info.NamedCertificates))

	switch info.BindNetwork {
	case "tcp", "tcp4", "tcp6":
	default:
		validationResults.AddErrors(field.Invalid(fldPath.Child("bindNetwork"), info.BindNetwork, "must be 'tcp', 'tcp4', or 'tcp6'"))
	}

	if len(info.CertFile) > 0 {
		if len(info.ClientCA) > 0 {
			validationResults.AddErrors(ValidateFile(info.ClientCA, fldPath.Child("clientCA"))...)
		}
	} else {
		if certificatesRequired && len(info.ClientCA) > 0 {
			validationResults.AddErrors(field.Invalid(fldPath.Child("clientCA"), info.ClientCA, "cannot specify a clientCA without a certFile"))
		}
	}

	if _, err := crypto.TLSVersion(info.MinTLSVersion); err != nil {
		validationResults.AddErrors(field.NotSupported(fldPath.Child("minTLSVersion"), info.MinTLSVersion, crypto.ValidTLSVersions()))
	}
	for i, cipher := range info.CipherSuites {
		if _, err := crypto.CipherSuite(cipher); err != nil {
			validationResults.AddErrors(field.NotSupported(fldPath.Child("cipherSuites").Index(i), cipher, crypto.ValidCipherSuites()))
		}
	}

	return validationResults
}

func ValidateNamedCertificates(fldPath *field.Path, namedCertificates []v1.NamedCertificate) ValidationResults {
	validationResults := ValidationResults{}

	takenNames := sets.NewString()
	for i, namedCertificate := range namedCertificates {
		idxPath := fldPath.Index(i)

		certDNSNames := []string{}
		if len(namedCertificate.CertFile) == 0 {
			validationResults.AddErrors(field.Required(idxPath.Child("certInfo"), ""))
		} else if certInfoErrors := ValidateCertInfo(namedCertificate.CertInfo, false, idxPath); len(certInfoErrors) > 0 {
			validationResults.AddErrors(certInfoErrors...)
		} else if cert, err := tls.LoadX509KeyPair(namedCertificate.CertFile, namedCertificate.KeyFile); err != nil {
			validationResults.AddErrors(field.Invalid(idxPath.Child("certInfo"), namedCertificate.CertInfo, fmt.Sprintf("error loading certificate/key: %v", err)))
		} else {
			leaf, _ := x509.ParseCertificate(cert.Certificate[0])
			certDNSNames = append(certDNSNames, leaf.Subject.CommonName)
			certDNSNames = append(certDNSNames, leaf.DNSNames...)
		}

		if len(namedCertificate.Names) == 0 {
			validationResults.AddErrors(field.Required(idxPath.Child("names"), ""))
		}
		for j, name := range namedCertificate.Names {
			jdxPath := idxPath.Child("names").Index(j)
			if len(name) == 0 {
				validationResults.AddErrors(field.Required(jdxPath, ""))
				continue
			}

			if takenNames.Has(name) {
				validationResults.AddErrors(field.Invalid(jdxPath, name, "this name is already used in another named certificate"))
				continue
			}

			// validate names as domain names or *.*.foo.com domain names
			validDNSName := true
			for _, s := range strings.Split(name, ".") {
				if s != "*" && len(utilvalidation.IsDNS1123Label(s)) != 0 {
					validDNSName = false
				}
			}
			if !validDNSName {
				validationResults.AddErrors(field.Invalid(jdxPath, name, "must be a valid DNS name"))
				continue
			}

			takenNames.Insert(name)

			// validate certificate has common name or subject alt names that match
			if len(certDNSNames) > 0 {
				foundMatch := false
				for _, dnsName := range certDNSNames {
					if HostnameMatches(dnsName, name) {
						foundMatch = true
						break
					}
					// if the cert has a wildcard dnsName, and we've configured a non-wildcard name, see if our specified name will match against the dnsName.
					if strings.HasPrefix(dnsName, "*.") && !strings.HasPrefix(name, "*.") && HostnameMatches(name, dnsName) {
						foundMatch = true
						break
					}
				}
				if !foundMatch {
					validationResults.AddWarnings(field.Invalid(jdxPath, name, "the specified certificate does not have a CommonName or DNS subjectAltName that matches this name"))
				}
			}
		}
	}

	return validationResults
}

func ValidateHostPort(value string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(value) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, ""))
	} else if _, _, err := net.SplitHostPort(value); err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, value, "must be a host:port"))
	}

	return allErrs
}

func ValidateCertInfo(certInfo v1.CertInfo, required bool, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if required {
		if len(certInfo.CertFile) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("certFile"), "The certificate file must be provided"))
		}
		if len(certInfo.KeyFile) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("keyFile"), "The certificate key must be provided"))
		}
	}

	if (len(certInfo.CertFile) == 0) != (len(certInfo.KeyFile) == 0) {
		allErrs = append(allErrs, field.Required(fldPath.Child("certFile"), "Both the certificate file and the certificate key must be provided together or not at all"))
		allErrs = append(allErrs, field.Required(fldPath.Child("keyFile"), "Both the certificate file and the certificate key must be provided together or not at all"))
	}

	if len(certInfo.CertFile) > 0 {
		allErrs = append(allErrs, ValidateFile(certInfo.CertFile, fldPath.Child("certFile"))...)
	}

	if len(certInfo.KeyFile) > 0 {
		allErrs = append(allErrs, ValidateFile(certInfo.KeyFile, fldPath.Child("keyFile"))...)
	}

	// validate certfile/keyfile load/parse?

	return allErrs
}

func ValidateFile(path string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if len(path) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, ""))
	} else if _, err := os.Stat(path); err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, path, fmt.Sprintf("could not read file: %v", err)))
	}

	return allErrs
}

func ValidateSecureURL(urlString string, fldPath *field.Path) (*url.URL, field.ErrorList) {
	url, urlErrs := ValidateURL(urlString, fldPath)
	if len(urlErrs) == 0 && url.Scheme != "https" {
		urlErrs = append(urlErrs, field.Invalid(fldPath, urlString, "must use https scheme"))
	}
	return url, urlErrs
}

func ValidateURL(urlString string, fldPath *field.Path) (*url.URL, field.ErrorList) {
	allErrs := field.ErrorList{}

	urlObj, err := url.Parse(urlString)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, urlString, "must be a valid URL"))
		return nil, allErrs
	}
	if len(urlObj.Scheme) == 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, urlString, "must contain a scheme (e.g. https://)"))
	}
	if len(urlObj.Host) == 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, urlString, "must contain a host"))
	}
	return urlObj, allErrs
}

func ValidateDir(path string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if len(path) == 0 {
		allErrs = append(allErrs, field.Required(fldPath, ""))
	} else {
		fileInfo, err := os.Stat(path)
		if err != nil {
			allErrs = append(allErrs, field.Invalid(fldPath, path, fmt.Sprintf("could not read info: %v", err)))
		} else if !fileInfo.IsDir() {
			allErrs = append(allErrs, field.Invalid(fldPath, path, "not a directory"))
		}
	}

	return allErrs
}

// HostnameMatchSpecCandidates returns a list of match specs that would match the provided hostname
// Returns nil if len(hostname) == 0
func HostnameMatchSpecCandidates(hostname string) []string {
	if len(hostname) == 0 {
		return nil
	}

	// Exact match has priority
	candidates := []string{hostname}

	// Replace successive labels in the name with wildcards, to require an exact match on number of
	// path segments, because certificates cannot wildcard multiple levels of subdomains
	//
	// This is primarily to be consistent with tls.Config#getCertificate implementation
	//
	// It using a cert signed for *.foo.example.com and *.bar.example.com by specifying the name *.*.example.com
	labels := strings.Split(hostname, ".")
	for i := range labels {
		labels[i] = "*"
		candidates = append(candidates, strings.Join(labels, "."))
	}
	return candidates
}

// HostnameMatches returns true if the given hostname is matched by the given matchSpec
func HostnameMatches(hostname string, matchSpec string) bool {
	return sets.NewString(HostnameMatchSpecCandidates(hostname)...).Has(matchSpec)
}
