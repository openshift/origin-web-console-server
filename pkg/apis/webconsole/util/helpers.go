package util

import "github.com/openshift/api/webconsole/v1"

func ResolveWebConsoleConfigurationPaths(config *v1.WebConsoleConfiguration, base string) error {
	return ResolvePaths(GetWebConsoleFileReferences(config), base)
}

func GetWebConsoleFileReferences(config *v1.WebConsoleConfiguration) []*string {
	refs := []*string{}

	refs = append(refs, &config.ServingInfo.CertFile)
	refs = append(refs, &config.ServingInfo.KeyFile)
	refs = append(refs, &config.ServingInfo.ClientCA)
	for i := range config.ServingInfo.NamedCertificates {
		refs = append(refs, &config.ServingInfo.NamedCertificates[i].CertFile)
		refs = append(refs, &config.ServingInfo.NamedCertificates[i].KeyFile)
	}

	for i := range config.ExtensionScripts {
		refs = append(refs, &config.ExtensionScripts[i])
	}
	for i := range config.ExtensionStylesheets {
		refs = append(refs, &config.ExtensionStylesheets[i])
	}
	for i := range config.Extensions {
		refs = append(refs, &config.Extensions[i].SourceDirectory)
	}

	return refs
}
