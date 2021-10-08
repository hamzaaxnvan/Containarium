package cmd

import (
	"os"

	"github.com/spf13/pflag"
)

// secretEnvFlag registers a string flag whose value falls back to the
// environment variable env, without ever advertising that value: pflag renders
// a flag's DefValue in usage and --help, so an env-derived default would print
