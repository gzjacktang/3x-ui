package mtproto

import (
	"sync"
	"testing"
)

func TestProcessConcurrentStatusStopAndRestart(t *testing.T) {
	pidFile := installFakeMtg(t)
	proc := newProcess(configPathForID(91), "race-test")
	readersDone := make(chan struct{})
	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for {
				select {
				case <-readersDone:
					return
				default:
					_ = proc.IsRunning()
					_ = proc.GetResult()
				}
			}
		})
	}
	t.Cleanup(func() {
		close(readersDone)
		readers.Wait()
		_ = proc.Stop()
	})
	for cycle := 1; cycle <= 3; cycle++ {
		if err := proc.Start(); err != nil {
			t.Fatal(err)
		}
		waitSpawnCount(t, pidFile, cycle)
		if !proc.IsRunning() {
			t.Fatal("started process is not running")
		}
		if err := proc.Start(); err == nil {
			t.Fatal("second Start unexpectedly succeeded")
		}
		stopped := make(chan error, 1)
		go func() { stopped <- proc.Stop() }()
		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
		if proc.IsRunning() {
			t.Fatal("stopped process is still running")
		}
	}
}
