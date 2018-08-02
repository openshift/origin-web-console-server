package validation

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/openshift/api/webconsole/v1"
)

// MinimumInactivityTimeoutMinutes defines the the smallest value allowed for InactivityTimeoutMinutes if not set to 0,
// which disables the feature.
const MinimumInactivityTimeoutMinutes = 5

// ValidateWebConsoleConfiguration validates the web console configuration properties.
func ValidateWebConsoleConfiguration(config *v1.WebConsoleConfiguration, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	validationResults.Append(ValidateHTTPServingInfo(config.ServingInfo, fldPath.Child("servingInfo")))
	validationResults.Append(validateClusterInfo(config.ClusterInfo, fldPath.Child("clusterInfo")))
	validationResults.Append(validateExtensions(config.Extensions, fldPath.Child("extensions")))
	validationResults.Append(validateFeatures(config.Features, fldPath.Child("features")))

	return validationResults
}

func validateClusterInfo(config v1.ClusterInfo, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	urlObj, urlErrs := ValidateURL(config.ConsolePublicURL, fldPath.Child("consolePublicURL"))
	if len(urlErrs) > 0 {
		validationResults.AddErrors(urlErrs...)
	}
	if urlObj != nil {
		if !strings.HasSuffix(urlObj.Path, "/") {
			validationResults.AddErrors(field.Invalid(fldPath.Child("consolePublicURL"), config.ConsolePublicURL, "must have a trailing slash in path"))
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

	if len(config.AdminConsolePublicURL) > 0 {
		_, urlErrs := ValidateSecureURL(config.AdminConsolePublicURL, fldPath.Child("adminConsolePublicURL"))
		if len(urlErrs) > 0 {
			validationResults.AddErrors(urlErrs...)
		}
	}

	if len(config.LogoutPublicURL) > 0 {
		_, urlErrs := ValidateURL(config.LogoutPublicURL, fldPath.Child("logoutPublicURL"))
		if len(urlErrs) > 0 {
			validationResults.AddErrors(urlErrs...)
		}
	}

	return validationResults
}

func validateExtensions(config v1.ExtensionsConfiguration, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	for i, scriptURL := range config.ScriptURLs {
		if _, scriptURLErrs := ValidateSecureURL(scriptURL, fldPath.Child("scripts").Index(i)); len(scriptURLErrs) > 0 {
			validationResults.AddErrors(scriptURLErrs...)
		}
	}

	for i, stylesheetURL := range config.StylesheetURLs {
		if _, stylesheetURLErrs := ValidateSecureURL(stylesheetURL, fldPath.Child("stylesheets").Index(i)); len(stylesheetURLErrs) > 0 {
			validationResults.AddErrors(stylesheetURLErrs...)
		}
	}

	return validationResults
}

func validateFeatures(config v1.FeaturesConfiguration, fldPath *field.Path) ValidationResults {
	validationResults := ValidationResults{}

	if config.InactivityTimeoutMinutes != 0 && config.InactivityTimeoutMinutes < MinimumInactivityTimeoutMinutes {
		validationResults.AddErrors(field.Invalid(
			fldPath.Child("inactivityTimeoutMinutes"),
			config.InactivityTimeoutMinutes,
			fmt.Sprintf("the minimum acceptable inactivity timeout value is %d minutes", MinimumInactivityTimeoutMinutes)))
	}

	return validationResults
}
