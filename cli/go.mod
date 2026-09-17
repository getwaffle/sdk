module github.com/very-good-labs/paybridge-cli

go 1.26.0

require (
	github.com/spf13/cobra v1.10.2
	github.com/very-good-labs/paybridge-go v0.0.0
	golang.org/x/term v0.46.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/very-good-labs/paybridge-go => ../go
