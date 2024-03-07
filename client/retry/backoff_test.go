// Copyright 2023 TiKV Project Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package retry

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pingcap/log"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBackoffer(t *testing.T) {
	re := require.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	base := time.Second
	max := 100 * time.Millisecond
	total := time.Millisecond
	// Test initial backoffer.
	bo := InitialBackoffer(base, max, total)
	// `bo.base` will be set to `bo.max` if `bo.base` is greater than `bo.max`.
	re.Equal(max, bo.base)
	re.Equal(max, bo.max)
	// `bo.total` will be set to `bo.base` if `bo.total` is greater than `bo.base`.
	re.Equal(bo.base, bo.total)

	base = 100 * time.Millisecond
	max = time.Second
	total = base
	// Test the same value of `bo.base` and `bo.total`.
	bo = InitialBackoffer(base, max, total)
	re.Equal(base, bo.base)
	re.Equal(total, bo.total)
	re.Equal(base, total)
	var (
		execCount   int
		expectedErr = errors.New("test")
	)
	err := bo.Exec(ctx, func() error {
		execCount++
		return expectedErr
	})
	re.ErrorIs(err, expectedErr)
	re.Equal(1, execCount)
	re.True(isBackofferReset(bo))

	base = 100 * time.Millisecond
	max = time.Second
	total = time.Second
	// Test the nextInterval function.
	bo = InitialBackoffer(base, max, total)
	re.Equal(bo.nextInterval(), base)
	re.Equal(bo.nextInterval(), 2*base)
	for i := 0; i < 10; i++ {
		re.LessOrEqual(bo.nextInterval(), max)
	}
	re.Equal(bo.nextInterval(), max)
	bo.resetBackoff()
	re.True(isBackofferReset(bo))

	// Test the total time cost.
	execCount = 0
	var start time.Time
	err = bo.Exec(ctx, func() error {
		execCount++
		if start.IsZero() {
			start = time.Now()
		}
		return expectedErr
	})
	re.InDelta(total, time.Since(start), float64(250*time.Millisecond))
	re.ErrorIs(err, expectedErr)
	re.Equal(4, execCount)
	re.True(isBackofferReset(bo))

	// Test the retryable checker.
	execCount = 0
	bo = InitialBackoffer(base, max, total)
	bo.SetRetryableChecker(func(err error) bool {
		return execCount < 2
	})
	err = bo.Exec(ctx, func() error {
		execCount++
		return nil
	})
	re.NoError(err)
	re.Equal(2, execCount)
	re.True(isBackofferReset(bo))
}

func isBackofferReset(bo *Backoffer) bool {
	return bo.next == bo.base && bo.currentTotal == 0
}

func TestBackofferWithLog(t *testing.T) {
	re := require.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conf := &log.Config{Level: "debug", File: log.FileLogConfig{}, DisableTimestamp: true}
	lg := newZapTestLogger(conf)
	log.ReplaceGlobals(lg.Logger, nil)

	bo := InitialBackoffer(time.Millisecond*10, time.Millisecond*100, time.Millisecond*1000, WithLogTimes(10))
	err := bo.Exec(ctx, testFn)
	re.ErrorIs(err, errTest)

	m1 := lg.Message()
	rfc := `["call PD API failed and retrying"] [api=testFn] [retry-time=10] [error=test]`
	re.Contains(m1, rfc)
}

var errTest = errors.New("test")

func testFn() error {
	return errTest
}

// testingWriter is a WriteSyncer that writes to the the messages.
type testingWriter struct {
	messages []string
}

func newTestingWriter() *testingWriter {
	return &testingWriter{}
}
func (w *testingWriter) Write(p []byte) (n int, err error) {
	n = len(p)
	p = bytes.TrimRight(p, "\n")
	m := string(p)
	w.messages = append(w.messages, m)
	return n, nil
}
func (w *testingWriter) Sync() error {
	return nil
}

type verifyLogger struct {
	*zap.Logger
	w *testingWriter
}

func (logger *verifyLogger) Message() string {
	if logger.w.messages == nil {
		return ""
	}
	return logger.w.messages[len(logger.w.messages)-1]
}

func newZapTestLogger(cfg *log.Config, opts ...zap.Option) verifyLogger {
	// TestingWriter is used to write to memory.
	// Used in the verify logger.
	writer := newTestingWriter()
	lg, _, _ := log.InitLoggerWithWriteSyncer(cfg, writer, writer, opts...)
	return verifyLogger{
		Logger: lg,
		w:      writer,
	}
}
