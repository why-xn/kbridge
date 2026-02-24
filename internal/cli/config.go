package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config keys
const (
	ConfigKeyCentralURL     = "central_url"
	ConfigKeyCurrentCluster = "current_cluster"
	ConfigKeyToken          = "token"
	ConfigKeyRefreshToken   = "refresh_token"
)

// configDir returns the kbridge config directory path
func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: unable to find home directory:", err)
		os.Exit(1)
	}
	return filepath.Join(home, ".kbridge")
}

// configFile returns the full path to the config file
func configFile() string {
	return filepath.Join(configDir(), "config.yaml")
}

// initConfig reads in config file and ENV variables if set
func initConfig() {
	// Set config file location
	viper.SetConfigFile(configFile())
	viper.SetConfigType("yaml")

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir(), 0755); err != nil {
		fmt.Fprintln(os.Stderr, "Error: unable to create config directory:", err)
	}

	// Set defaults
	viper.SetDefault(ConfigKeyCentralURL, "")
	viper.SetDefault(ConfigKeyCurrentCluster, "")
	viper.SetDefault(ConfigKeyToken, "")
	viper.SetDefault(ConfigKeyRefreshToken, "")

	// Read config file if it exists
	if err := viper.ReadInConfig(); err != nil {
		// It's okay if the config file doesn't exist yet
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Only report error if it's not a "file not found" error
			if !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "Warning: error reading config file:", err)
			}
		}
	}
}

// saveConfig writes the current configuration to the config file
func saveConfig() error {
	return viper.WriteConfigAs(configFile())
}
