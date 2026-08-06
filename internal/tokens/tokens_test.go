package tokens

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkoukk/tiktoken-go"
)

func TestCountIsNonZeroForProseAndZeroForEmpty(t *testing.T) {
	n, err := Count("The quick brown fox jumps over the lazy dog.")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n < 5 || n > 20 {
		t.Fatalf("expected a plausible BPE count for a 9-word sentence, got %d", n)
	}

	z, err := Count("")
	if err != nil {
		t.Fatalf("Count(empty): %v", err)
	}
	if z != 0 {
		t.Fatalf("empty string must be 0 tokens, got %d", z)
	}
}

func TestCountLinesEqualsSumOfParts(t *testing.T) {
	a, _ := Count("- alpha: does the alpha thing\n")
	b, _ := Count("- beta: does the beta thing\n")
	got, err := CountLines([]string{
		"- alpha: does the alpha thing\n",
		"- beta: does the beta thing\n",
	})
	if err != nil {
		t.Fatalf("CountLines: %v", err)
	}
	if got != a+b {
		t.Fatalf("CountLines = %d, want %d (a=%d b=%d)", got, a+b, a, b)
	}
}

// resetEncoderState clears the package-level memoisation so a test can
// observe a fresh warm. Only safe to call when no warm goroutine from a
// prior test is still running unsynchronized - callers that override warmFn
// with something that blocks must release and wait for it first.
func resetEncoderState() {
	encOnce = sync.Once{}
	enc = nil
	encErr = nil
	warmDone = nil
}

// TestEncoderTimeout pins the fetch-timeout guard: a warm that never
// completes must make Encoder return an error within ~2x the (test-shrunk)
// deadline rather than block indefinitely.
func TestEncoderTimeout(t *testing.T) {
	origWarm := warmFn
	origTimeout := fetchTimeout
	release := make(chan struct{})

	defer func() {
		close(release)
		if warmDone != nil {
			<-warmDone // wait for the stub goroutine before resetting shared state
		}
		warmFn = origWarm
		fetchTimeout = origTimeout
		resetEncoderState()
	}()

	resetEncoderState()
	fetchTimeout = 50 * time.Millisecond
	warmFn = func() (*tiktoken.Tiktoken, error) {
		<-release // simulates a fetch that never returns within the deadline
		return nil, nil
	}

	start := time.Now()
	_, err := Encoder()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a timeout error, got nil")
	}
	if elapsed > 2*fetchTimeout {
		t.Fatalf("Encoder blocked for %s, want <= %s (2x the deadline)", elapsed, 2*fetchTimeout)
	}
}

// TestEncoderWarmMemoizedOnce proves the happy path: concurrent callers all
// get the same result and the underlying warm runs exactly once, never once
// per call. Run with -race to prove the goroutine/channel handoff is safe.
func TestEncoderWarmMemoizedOnce(t *testing.T) {
	origWarm := warmFn
	defer func() {
		warmFn = origWarm
		resetEncoderState()
	}()
	resetEncoderState()

	var calls int32
	stub := &tiktoken.Tiktoken{}
	warmFn = func() (*tiktoken.Tiktoken, error) {
		atomic.AddInt32(&calls, 1)
		return stub, nil
	}

	const n = 10
	var wg sync.WaitGroup
	results := make([]*tiktoken.Tiktoken, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = Encoder()
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("warmFn called %d times, want exactly 1", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("result[%d]: unexpected error %v", i, errs[i])
		}
		if results[i] != stub {
			t.Fatalf("result[%d] = %p, want %p", i, results[i], stub)
		}
	}
}
