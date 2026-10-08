package app

import "testing"

// cpuBusyPercent is the one piece of the CPU meter that every platform shares,
// and the only one CI can check for Windows and the BSDs, which it cannot run.
func TestCPUBusyPercent(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur cpuTicks
		want      float64
	}{
		{"quarter busy", cpuTicks{idle: 100, total: 200}, cpuTicks{idle: 175, total: 300}, 25},
		{"all idle", cpuTicks{idle: 0, total: 0}, cpuTicks{idle: 50, total: 50}, 0},
		{"all busy", cpuTicks{idle: 10, total: 10}, cpuTicks{idle: 10, total: 90}, 100},
		{"no time passed", cpuTicks{idle: 5, total: 9}, cpuTicks{idle: 5, total: 9}, 0},
		{"total went back", cpuTicks{idle: 5, total: 90}, cpuTicks{idle: 6, total: 10}, 0},
		{"idle went back", cpuTicks{idle: 50, total: 90}, cpuTicks{idle: 10, total: 100}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cpuBusyPercent(c.prev, c.cur); got != c.want {
				t.Fatalf("cpuBusyPercent(%+v, %+v) = %v, want %v", c.prev, c.cur, got, c.want)
			}
		})
	}
}
