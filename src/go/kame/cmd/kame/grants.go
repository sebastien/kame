package main

import (
	"kame/lang/eval"
	"slices"
)

func hasGrant(grants []eval.Grant, capability eval.Capability) bool {
	return slices.ContainsFunc(grants, func(grant eval.Grant) bool { return grant.Capability == capability })
}

func effectiveGrants(explicit, defaults []eval.Grant, explicitOnly bool) []eval.Grant {
	if explicitOnly {
		return explicit
	}
	grants := slices.Clone(explicit)
	for _, grant := range defaults {
		if !hasGrant(explicit, grant.Capability) {
			grants = append(grants, grant)
		}
	}
	return grants
}
