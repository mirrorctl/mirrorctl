package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/cybozu-go/aptutil/mirror"
	"log/slog"
)

const (
	defaultConfigPath = "/etc/apt/mirror.toml"
)

var (
	configPath = flag.String("f", defaultConfigPath, "configuration file name")
)

func main() {
	flag.Parse()

	config := mirror.NewConfig()
	metadata, err := toml.DecodeFile(*configPath, config)
	if err != nil {
		slog.Error("failed to decode config file", "error", err)
		os.Exit(1)
	}
	if len(metadata.Undecoded()) > 0 {
		slog.Error("invalid config keys", "keys", fmt.Sprintf("%#v", metadata.Undecoded()))
		os.Exit(1)
	}

	err = config.Log.Apply()
	if err != nil {
		slog.Error("failed to apply log config", "error", err)
		os.Exit(1)
	}

	err = mirror.Run(config, flag.Args())
	if err != nil {
		slog.Error("mirror run failed", "error", err)
		os.Exit(1)
	}
}
