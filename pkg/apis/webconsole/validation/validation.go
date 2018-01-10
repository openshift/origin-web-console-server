package validation

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/openshift/api/webconsole/v1"
)

func ValidateWebConsoleConfiguration(config *v1.WebConsoleConfiguration, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	validationResults.Append(ValidateHTTPServingInfo(config.ServingInfo, fldPath.Child("servingInfo")))

	if len(config.LogoutURL) > 0 {
		_, urlErrs := ValidateURL(config.LogoutURL, fldPath.Child("logoutURL"))
		if len(urlErrs) > 0 {
			validationResults.AddErrors(urlErrs...)
		}
	}

	urlObj, urlErrs := ValidateURL(config.PublicURL, fldPath.Child("publicURL"))
	if len(urlErrs) > 0 {
		validationResults.AddErrors(urlErrs...)
	}
	if urlObj != nil {
		if !strings.HasSuffix(urlObj.Path, "/") {
			validationResults.AddErrors(field.Invalid(fldPath.Child("publicURL"), config.PublicURL, "must have a trailing slash in path"))
		}
	}

	if _, urlErrs := ValidateURL(config.MasterPublicURL, fldPath.Child("masterPublicURL")); len(urlErrs) > 0 {
		validationResults.AddErrors(urlErrs...)
	}

	if len(config.LoggingPublicURL) > 0 {
		if _, loggingURLErrs := ValidateSecureURL(config.LoggingPublicURL, fldPath.Child("loggingPublicURL")); len(loggingURLErrs) > 0 {
			validationResults.AddErrors(loggingURLErrs...)
		}
	} else {
		validationResults.AddWarnings(field.Invalid(fldPath.Child("loggingPublicURL"), "", "required to view aggregated container logs in the console"))
	}

	if len(config.MetricsPublicURL) > 0 {
		if _, metricsURLErrs := ValidateSecureURL(config.MetricsPublicURL, fldPath.Child("metricsPublicURL")); len(metricsURLErrs) > 0 {
			validationResults.AddErrors(metricsURLErrs...)
		}
	} else {
		validationResults.AddWarnings(field.Invalid(fldPath.Child("metricsPublicURL"), "", "required to view cluster metrics in the console"))
	}

	// FIXME: Temporarily turn off validation since these are now treated as URLs.
	//        We will fix when we update the AssetConfig for the new extension script URL / style URL property names.
	// for i, scriptFile := range config.ExtensionScripts {
	// 	validationResults.AddErrors(ValidateFile(scriptFile, fldPath.Child("extensionScripts").Index(i))...)
	// }

	// for i, stylesheetFile := range config.ExtensionStylesheets {
	// 	validationResults.AddErrors(ValidateFile(stylesheetFile, fldPath.Child("extensionStylesheets").Index(i))...)
	// }

	nameTaken := map[string]bool{}
	for i, extConfig := range config.Extensions {
		idxPath := fldPath.Child("extensions").Index(i)
		extConfigErrors := ValidateAssetExtensionsConfig(extConfig, idxPath)
		validationResults.AddErrors(extConfigErrors...)
		if nameTaken[extConfig.Name] {
			dupError := field.Invalid(idxPath.Child("name"), extConfig.Name, "duplicate extension name")
			validationResults.AddErrors(dupError)
		} else {
			nameTaken[extConfig.Name] = true
		}
	}

	return validationResults
}

var extNameExp = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func ValidateAssetExtensionsConfig(extConfig v1.AssetExtensionsConfig, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, ValidateDir(extConfig.SourceDirectory, fldPath.Child("sourceDirectory"))...)

	if len(extConfig.Name) == 0 {
		allErrs = append(allErrs, field.Required(fldPath.Child("name"), ""))
	} else if !extNameExp.MatchString(extConfig.Name) {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("name"), extConfig.Name, fmt.Sprintf("does not match %v", extNameExp)))
	}

	return allErrs
}
