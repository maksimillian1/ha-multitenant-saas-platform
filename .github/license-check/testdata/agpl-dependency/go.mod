// Negative fixture for check.sh: a module that depends on an AGPL module.
// The check must fail on it (C-03, HAMSP-21).
module example.com/licensefixture

go 1.24

require example.com/agpl v0.0.0

replace example.com/agpl => ./agpl
