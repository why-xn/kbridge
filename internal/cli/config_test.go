package cli

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

func TestSaveConfigPermissions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	initConfig()
	if err := os.WriteFile(configFile(), []byte("token: x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	viper.Set(ConfigKeyToken, "secret")
	if err := saveConfig(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{configDir(): 0700, configFile(): 0600} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s mode = %v (err %v), want %v", path, fi.Mode().Perm(), err, want)
		}
	}
}
