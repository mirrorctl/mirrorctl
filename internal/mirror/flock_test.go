package mirror

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestFlock(t *testing.T) {
	t.Parallel()

	// Create a context with a timeout to prevent hangs
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel() // Important to release resources

	// Use CommandContext to associate the command with the timeout
	cmd := exec.CommandContext(ctx, "flock", "testdata/mirror.toml", "sleep", "0.2")
	err := cmd.Start()
	if err != nil {
		t.Skip()
		return
	}
	time.Sleep(100 * time.Millisecond)

	f, err := os.Open("testdata/mirror.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fl := Flock{f}
	if err = fl.Lock(); err == nil {
		t.Error(`err = fl.Lock(); err == nil`)
	} else {
		t.Log(err)
	}

	err = cmd.Wait()
	// Check if the error was due to the timeout
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("test timed out waiting for external flock command")
	}
	if err != nil {
		t.Logf("external flock command exited with error: %v", err)
	}

	if err = fl.Lock(); err != nil {
		t.Fatal(err)
	}
	if err = fl.Unlock(); err != nil {
		t.Error(err)
	}
}
