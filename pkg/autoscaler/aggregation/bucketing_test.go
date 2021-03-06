/*
Copyright 2019 The Knative Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aggregation

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

const (
	granularity = time.Second
	pod         = "pod"
)

func TestTimedFloat64BucketsSimple(t *testing.T) {
	trunc1 := time.Now().Truncate(1 * time.Second)
	trunc5 := time.Now().Truncate(5 * time.Second)

	type args struct {
		time  time.Time
		name  string
		value float64
	}
	tests := []struct {
		name        string
		granularity time.Duration
		stats       []args
		want        map[time.Time]float64
	}{{
		name:        "granularity = 1s",
		granularity: time.Second,
		stats: []args{
			{trunc1, pod, 1.0}, // activator scale from 0.
			{trunc1.Add(100 * time.Millisecond), pod, 10.0}, // from scraping pod/sent by activator.
			{trunc1.Add(1 * time.Second), pod, 1.0},         // next bucket
			{trunc1.Add(3 * time.Second), pod, 1.0},         // nextnextnext bucket
		},
		want: map[time.Time]float64{
			trunc1:                      11.0,
			trunc1.Add(1 * time.Second): 1.0,
			trunc1.Add(3 * time.Second): 1.0,
		},
	}, {
		name:        "granularity = 5s",
		granularity: 5 * time.Second,
		stats: []args{
			{trunc5, pod, 1.0},
			{trunc5.Add(3 * time.Second), pod, 11.0}, // same bucket
			{trunc5.Add(6 * time.Second), pod, 1.0},  // next bucket
		},
		want: map[time.Time]float64{
			trunc5:                      12.0,
			trunc5.Add(5 * time.Second): 1.0,
		},
	}, {
		name:        "empty",
		granularity: time.Second,
		stats:       []args{},
		want:        map[time.Time]float64{},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// New implementation test.
			buckets := NewTimedFloat64Buckets(2*time.Minute, tt.granularity)
			for _, stat := range tt.stats {
				buckets.Record(stat.time, stat.name, stat.value)
			}

			got := make(map[time.Time]float64)
			// Less time in future than our window is (2mins above), but more than any of the tests report.
			buckets.ForEachBucket(trunc1.Add(time.Minute), func(t time.Time, b float64) {
				// Since we're storing 0s when there's no data, we need to exclude those
				// for this test.
				if b > 0 {
					got[t] = b
				}
			})

			if !cmp.Equal(tt.want, got) {
				t.Errorf("Unexpected values (-want +got): %v", cmp.Diff(tt.want, got))
			}
		})
	}
}

func TestTimedFloat64BucketsManyReps(t *testing.T) {
	trunc1 := time.Now().Truncate(granularity)
	buckets := NewTimedFloat64Buckets(time.Minute, granularity)
	for p := 0; p < 5; p++ {
		trunc1 = trunc1.Add(granularity)
		for t := 0; t < 5; t++ {
			buckets.Record(trunc1, pod+strconv.Itoa(p), float64(p+t))
		}
	}
	// So the buckets are:
	// [0, 1, 2, 3, 4] = 10
	// [1, 2, 3, 4, 5] = 15
	// ...						 = ...
	// [4, 5, 6, 7, 8]  = 30
	//                  = 100
	const want = 100.
	sum1, sum2 := 0., 0.
	buckets.ForEachBucket(trunc1, func(_ time.Time, b float64) {
		sum1 += b
	})
	buckets.ForEachBucket(trunc1, func(_ time.Time, b float64) {
		sum2 += b
	})
	if got, want := sum1, want; got != want {
		t.Errorf("Sum1 = %f, want: %f", got, want)
	}

	if got, want := sum2, want; got != want {
		t.Errorf("Sum2 = %f, want: %f", got, want)
	}
}

func TestTimedFloat64BucketsHoles(t *testing.T) {
	now := time.Now()
	buckets := NewTimedFloat64Buckets(5*time.Second, granularity)

	for i := time.Duration(0); i < 5; i++ {
		buckets.Record(now.Add(i*time.Second), pod, float64(i+1))
	}

	sum := 0.

	if !buckets.ForEachBucket(now.Add(4*time.Second),
		func(_ time.Time, b float64) {
			sum += b
		},
	) {
		t.Fatal("ForEachBucket unexpectedly returned empty result")
	}
	if got, want := sum, 15.; got != want {
		t.Errorf("Sum = %v, want: %v", got, want)
	}
	// Now write at 9th second. Which means that seconds
	// 5[0], 6[1], 7[2] become 0.
	buckets.Record(now.Add(8*time.Second), pod, 2.)
	// So now we have [3] = 2, [4] = 5 and sum should be 7.
	sum = 0.

	if !buckets.ForEachBucket(now.Add(8*time.Second),
		func(_ time.Time, b float64) {
			sum += b
		},
	) {
		t.Fatal("ForEachBucket unexpectedly returned empty result")
	}
	if got, want := sum, 7.; got != want {
		t.Errorf("Sum = %v, want: %v", got, want)
	}
}

func TestTimedFloat64BucketsForEachBucket(t *testing.T) {
	now := time.Now()
	buckets := NewTimedFloat64Buckets(2*time.Minute, granularity)

	// Since we recorded 0 data, even in this implementation no iteration must occur.
	if buckets.ForEachBucket(now, func(time time.Time, bucket float64) {}) {
		t.Fatalf("ForEachBucket unexpectedly returned non-empty result")
	}

	buckets.Record(now, pod, 10.0)
	buckets.Record(now.Add(1*time.Second), pod, 10.0)
	buckets.Record(now.Add(2*time.Second), pod, 5.0)
	buckets.Record(now.Add(3*time.Second), pod, 5.0)

	acc1 := 0
	acc2 := 0

	if !buckets.ForEachBucket(now.Add(4*time.Second),
		func(_ time.Time, b float64) {
			// We need to exclude the 0s for this test.
			if b > 0 {
				acc1++
			}
		},
		func(_ time.Time, b float64) {
			if b > 0 {
				acc2++
			}
		},
	) {
		t.Fatal("ForEachBucket unexpectedly returned empty result")
	}
}

func TestTimedFloat64BucketsWindowUpdate(t *testing.T) {
	now := time.Now()
	buckets := NewTimedFloat64Buckets(5*time.Second, granularity)

	// Fill the whole bucketing list with rollover.
	buckets.Record(now, pod, 1)
	buckets.Record(now.Add(1*time.Second), pod, 2)
	buckets.Record(now.Add(2*time.Second), pod, 3)
	buckets.Record(now.Add(3*time.Second), pod, 4)
	buckets.Record(now.Add(4*time.Second), pod, 5)
	buckets.Record(now.Add(5*time.Second), pod, 6)
	sum := 0.
	buckets.ForEachBucket(now.Add(5*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	if got, want := sum, float64(2+3+4+5+6); got != want {
		t.Fatalf("Initial data set Sum = %v, want: %v", got, want)
	}

	// Increase window.
	buckets.ResizeWindow(10 * time.Second)
	if got, want := len(buckets.buckets), 10; got != want {
		t.Fatalf("Resized bucket count = %d, want: %d", got, want)
	}
	if got, want := buckets.window, 10*time.Second; got != want {
		t.Fatalf("Resized bucket windos = %v, want: %v", got, want)
	}

	// Verify values were properly copied.
	sum = 0.
	buckets.ForEachBucket(now.Add(5*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	if got, want := sum, float64(2+3+4+5+6); got != want {
		t.Fatalf("After first resize data set Sum = %v, want: %v", got, want)
	}
	// Add one more. Make sure all the data is preserved, since window is longer.
	buckets.Record(now.Add(6*time.Second), pod, 7)
	sum = 0.
	buckets.ForEachBucket(now.Add(6*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	if got, want := sum, float64(2+3+4+5+6+7); got != want {
		t.Fatalf("Updated data set Sum = %v, want: %v", got, want)
	}

	// Now let's reduce window size.
	buckets.ResizeWindow(4 * time.Second)
	if got, want := len(buckets.buckets), 4; got != want {
		t.Fatalf("Resized bucket count = %d, want: %d", got, want)
	}
	// Just last 4 buckets should have remained.
	sum = 0.
	buckets.ForEachBucket(now.Add(6*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	if got, want := sum, float64(4+5+6+7); got != want {
		t.Fatalf("Updated data set Sum = %v, want: %v", got, want)
	}

	// Verify idempotence.
	ob := &buckets.buckets
	buckets.ResizeWindow(4 * time.Second)
	if ob != &buckets.buckets {
		t.Error("The buckets have changed, though window didn't")
	}
}

func TestTimedFloat64BucketsWindowUpdate3sGranularity(t *testing.T) {
	granularity := 3 * time.Second
	trunc1 := time.Now().Truncate(granularity)

	// So two buckets here (ceil(5/3)=ceil(1.6(6))=2).
	buckets := NewTimedFloat64Buckets(5*time.Second, granularity)
	if got, want := len(buckets.buckets), 2; got != want {
		t.Fatalf("Initial bucket count = %d, want: %d", got, want)
	}

	// Fill the whole bucketing list.
	buckets.Record(trunc1, pod, 10)
	buckets.Record(trunc1.Add(1*time.Second), pod, 2)
	buckets.Record(trunc1.Add(2*time.Second), pod, 3)
	buckets.Record(trunc1.Add(3*time.Second), pod, 4)
	buckets.Record(trunc1.Add(4*time.Second), pod, 5)
	buckets.Record(trunc1.Add(5*time.Second), pod, 6)
	buckets.Record(trunc1.Add(6*time.Second), pod, 7) // This overrides the initial 15 (10+2+3)
	sum := 0.
	buckets.ForEachBucket(trunc1.Add(6*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	want := (4. + 5 + 6) + 7
	if got, want := sum, want; got != want {
		t.Fatalf("Initial data set Sum = %v, want: %v", got, want)
	}

	// Increase window.
	buckets.ResizeWindow(10 * time.Second)
	if got, want := len(buckets.buckets), 4; got != want {
		t.Fatalf("Resized bucket count = %d, want: %d", got, want)
	}
	if got, want := buckets.window, 10*time.Second; got != want {
		t.Fatalf("Resized bucket windos = %v, want: %v", got, want)
	}

	// Verify values were properly copied.
	sum = 0
	buckets.ForEachBucket(trunc1.Add(6*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	if got, want := sum, want; got != want {
		t.Fatalf("After first resize data set Sum = %v, want: %v", got, want)
	}

	// Add one more. Make sure all the data is preserved, since window is longer.
	buckets.Record(trunc1.Add(9*time.Second+300*time.Millisecond), pod, 42)
	sum = 0
	buckets.ForEachBucket(trunc1.Add(9*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	want += 42
	if got, want := sum, want; got != want {
		t.Fatalf("Updated data set Sum = %v, want: %v", got, want)
	}

	// Now let's reduce window size.
	buckets.ResizeWindow(4 * time.Second)

	sum = 0
	if got, want := len(buckets.buckets), 2; got != want {
		t.Fatalf("Resized bucket count = %d, want: %d", got, want)
	}
	// Just last 4 buckets should have remained.
	sum = 0.
	want = 42 + 7 // we drop oldest bucket and the one not yet utilizied)
	buckets.ForEachBucket(trunc1.Add(9*time.Second), func(t time.Time, b float64) {
		sum += b
	})
	if got, want := sum, want; got != want {
		t.Fatalf("Updated data set Sum = %v, want: %v", got, want)
	}

	// Verify idempotence.
	ob := &buckets.buckets
	buckets.ResizeWindow(4 * time.Second)
	if ob != &buckets.buckets {
		t.Error("The buckets have changed, though window didn't")
	}
}
