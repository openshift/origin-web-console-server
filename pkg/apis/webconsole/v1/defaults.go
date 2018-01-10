package v1

import (
	"github.com/openshift/api/webconsole/v1"
)

func SetDefaults_ServingInfo(obj *v1.ServingInfo) {
	if len(obj.BindNetwork) == 0 {
		obj.BindNetwork = "tcp4"
	}
}

// TODO this needs to be removed and we need to use the real scheme defaulting ability, but right
// now this repo has no generation at all
func SetDefaults_WebConsoleConfiguration(obj *v1.WebConsoleConfiguration) {
	SetDefaults_ServingInfo(&obj.ServingInfo.ServingInfo)
}
