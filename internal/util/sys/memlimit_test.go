package sys

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestCgroupMemoryLimitAt(t *testing.T) {
	const service = "/system.slice/x-ui.service"
	for _, tc := range []struct {
		name, self string
		files      map[string]string
		want       int64
	}{
		{
			name: "systemd service inside a larger container",
			self: "0::" + service + "\n",
			files: map[string]string{
				"root/memory.max":                "2147483648\n",
				"root/system.slice/memory.max":   "max\n",
				"root" + service + "/memory.max": "681574400\n",
			},
			want: 681574400,
		},
		{
			name: "bare host with no root memory controller file",
			self: "0::" + service + "\n",
			files: map[string]string{
				"root" + service + "/memory.max": "681574400\n",
			},
			want: 681574400,
		},
		{
			name: "slice is tighter than service and container",
			self: "0::" + service + "\n",
			files: map[string]string{
				"root/memory.max":                "2147483648\n",
				"root/system.slice/memory.max":   "536870912\n",
				"root" + service + "/memory.max": "681574400\n",
			},
			want: 536870912,
		},
		{
			name:  "Docker private cgroup namespace retains root limit",
			self:  "0::/\n",
			files: map[string]string{"root/memory.max": "268435456\n"},
			want:  268435456,
		},
		{
			name: "v1 memory controller among several controllers",
			self: "12:cpu,cpuacct:/other\n7:memory,hugetlb:" + service + "\n",
			files: map[string]string{
				"root/memory/memory.limit_in_bytes":                "9223372036854771712\n",
				"root/memory" + service + "/memory.limit_in_bytes": "681574400\n",
			},
			want: 681574400,
		},
		{
			name: "v1 parent limit is tighter",
			self: "7:memory:" + service + "\n",
			files: map[string]string{
				"root/memory/memory.limit_in_bytes":                "268435456\n",
				"root/memory" + service + "/memory.limit_in_bytes": "681574400\n",
			},
			want: 268435456,
		},
		{
			name: "malformed or unlimited files do not invent a budget",
			self: "0::" + service + "\n",
			files: map[string]string{
				"root/memory.max":                "max\n",
				"root/system.slice/memory.max":   "-1\n",
				"root" + service + "/memory.max": "not-a-number\n",
			},
		},
		{
			name: "namespace traversal never reads above visible root",
			self: "0::/../../elsewhere\n",
			files: map[string]string{
				"memory.max":      "1048576\n",
				"root/memory.max": "2147483648\n",
			},
			want: 2147483648,
		},
		{
			name:  "missing proc entry preserves legacy root fallback",
			files: map[string]string{"root/memory.max": "268435456\n"},
			want:  268435456,
		},
		{name: "non Linux or no cgroup files"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, contents := range tc.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, ok := cgroupMemoryLimitAt(tc.self, filepath.Join(dir, "root"))
			if got != tc.want || ok != (tc.want > 0) {
				t.Fatalf("limit = %d, %v; want %d, %v", got, ok, tc.want, tc.want > 0)
			}
		})
	}
}

func TestApplyMemoryLimitHonorsExplicitBudgets(t *testing.T) {
	previous := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(previous) })
	t.Setenv("GOMEMLIMIT", "256MiB")
	t.Setenv("XUI_MEMORY_LIMIT", "128")
	if limit, source := applyMemoryLimit(); limit != 0 || source != "GOMEMLIMIT env (handled by the Go runtime)" {
		t.Fatalf("GOMEMLIMIT must retain precedence: %d, %q", limit, source)
	}
	if got := debug.SetMemoryLimit(-1); got != previous {
		t.Fatalf("explicit GOMEMLIMIT handling changed the running limit: got %d, want %d", got, previous)
	}
	t.Setenv("GOMEMLIMIT", "")
	if limit, source := applyMemoryLimit(); limit != 128<<20 || source != "XUI_MEMORY_LIMIT=128MiB" {
		t.Fatalf("XUI_MEMORY_LIMIT was not applied: %d, %q", limit, source)
	}
	if got := debug.SetMemoryLimit(-1); got != 128<<20 {
		t.Fatalf("runtime limit = %d, want %d", got, int64(128<<20))
	}
}
