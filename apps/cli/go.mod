module github.com/jp-cloud/jp

go 1.22

require (
	github.com/jp-cloud/go-common v0.0.0
	github.com/spf13/cobra v1.8.1
	golang.org/x/term v0.27.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/pretty v0.3.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/sys v0.28.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

replace github.com/jp-cloud/go-common => ../../packages/go-common
