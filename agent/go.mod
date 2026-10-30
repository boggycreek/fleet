module github.com/boggycreek/fleet/agent

go 1.27.1

require (
	github.com/boggycreek/fleet/backplane v0.0.0
	github.com/boggycreek/fleet/mcp v0.0.0
)

replace (
	github.com/boggycreek/fleet/backplane => ../backplane
	github.com/boggycreek/fleet/mcp => ../mcp
)
