package sessions

import "strings"

// modelBindingMatches permits only the named provider's optional qualification.
// Organization paths and model variants remain part of the exact model ID.
func modelBindingMatches(provider, model, readProvider, readModel string) bool {
	if model == "" || readModel == "" {
		return false
	}
	if provider != "" && readProvider != "" && provider != readProvider {
		return false
	}
	qualification := provider
	if qualification == "" {
		qualification = readProvider
	}
	if qualification != "" {
		model = strings.TrimPrefix(model, qualification+"/")
		readModel = strings.TrimPrefix(readModel, qualification+"/")
	}
	return model != "" && model == readModel
}
