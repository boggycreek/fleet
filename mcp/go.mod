module github.com/boggycreek/fleet/mcp

go 1.27.1

require (
	github.com/boggycreek/fleet/backplane v0.0.0
	github.com/boggycreek/fleet/cli v0.0.0
)

replace github.com/boggycreek/fleet/cli => ../cli
replace github.com/boggycreek/fleet/backplane => ../backplane
