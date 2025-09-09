package mirror

import (
	"context"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

func TestMirror(t *testing.T) {
	t.Parallel()

	c := new(Config)
	_, err := toml.DecodeFile("testdata/mirror.toml", c)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewMirror(time.Now(), "hogehoge", c, false, false, false); err == nil {
		t.Error(`_, err := NewMirror(time.Now(), "hogehoge", c); err == nil`)
	}

	t.Skip()

	m, err := NewMirror(time.Now(), "ubuntu", c, false, false, false)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	err = m.Update(ctx)
	if err != nil {
		t.Error(err)
	}
}
