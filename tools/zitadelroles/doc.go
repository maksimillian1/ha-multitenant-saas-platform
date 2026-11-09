// Package zitadelroles holds one test: the Zitadel role mappings in
// prototype/k8s/apps/zitadel/values.yaml are Zitadel's defaults plus our own
// roles, nothing else (feature admin-zitadel, AC-15).
//
// Zitadel's config replaces its default list of role mappings as a whole, so
// the values file carries a copy of it. When the chart version in
// prototype/k8s/apps/zitadel/Chart.yaml changes, copy the
// InternalAuthZ.RolePermissionMappings of that Zitadel version's
// cmd/defaults.yaml into testdata/chart-<version>.yaml and into the values.
package zitadelroles
