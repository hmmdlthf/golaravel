package queue

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/golaravel/golaravel/pkg/context"
	"github.com/golaravel/golaravel/pkg/router"
)

// MountDashboard registers the Horizon-equivalent Worker Dashboard and API routes.
func MountDashboard(r router.RouteRegistrar, prefix ...string) {
	base := "/queue"
	if len(prefix) > 0 && prefix[0] != "" {
		base = strings.TrimRight(prefix[0], "/")
	}

	d := DefaultQueue()

	// Web UI Dashboard
	r.Get(base+"/dashboard", func(c *context.Context) error {
		snap := d.metrics.Snapshot()
		failedCount, _ := d.dlq.Count()

		html := renderDashboardHTML(base, snap, failedCount)
		return c.HTML(http.StatusOK, html)
	})

	// JSON APIs
	r.Get(base+"/api/stats", func(c *context.Context) error {
		snap := d.metrics.Snapshot()
		sizes := make(map[string]int64)
		for _, q := range []string{"default", "high", "low"} {
			if s, err := d.driver.Size(q); err == nil {
				sizes[q] = s
			}
		}
		failedCount, _ := d.dlq.Count()

		return c.JSON(http.StatusOK, map[string]any{
			"snapshot":     snap,
			"queue_sizes":  sizes,
			"failed_count": failedCount,
		})
	})

	r.Get(base+"/api/failed", func(c *context.Context) error {
		jobs, err := d.dlq.All(50, 0)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, jobs)
	})

	r.Post(base+"/api/retry/:id", func(c *context.Context) error {
		id := c.Param("id")
		if err := d.RetryFailed(id); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"status":  "retried",
			"message": fmt.Sprintf("Failed job %s pushed back to queue", id),
		})
	})

	r.Post(base+"/api/retry-all", func(c *context.Context) error {
		count, err := d.RetryAllFailed()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"status":  "retried_all",
			"count":   count,
			"message": fmt.Sprintf("Retried %d failed jobs", count),
		})
	})

	r.Delete(base+"/api/failed/:id", func(c *context.Context) error {
		id := c.Param("id")
		if err := d.dlq.Delete(id); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"status": "deleted",
		})
	})

	r.Post(base+"/api/flush", func(c *context.Context) error {
		if err := d.dlq.Flush(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"status": "flushed",
		})
	})
}

func renderDashboardHTML(base string, snap Snapshot, failedCount int64) string {
	var workersRows strings.Builder
	for _, w := range snap.Workers {
		statusBadge := `<span class="px-2.5 py-0.5 rounded-full text-xs font-semibold bg-emerald-500/20 text-emerald-400 border border-emerald-500/30">IDLE</span>`
		if w.Status == "busy" {
			statusBadge = `<span class="px-2.5 py-0.5 rounded-full text-xs font-semibold bg-amber-500/20 text-amber-300 border border-amber-500/30 animate-pulse">BUSY</span>`
		}
		current := w.Current
		if current == "" {
			current = "-"
		}
		workersRows.WriteString(fmt.Sprintf(`
			<tr class="hover:bg-slate-700/30 border-b border-slate-700/50">
				<td class="px-6 py-3 font-mono text-xs text-sky-400">Worker #%d</td>
				<td class="px-6 py-3">%s</td>
				<td class="px-6 py-3 font-medium text-slate-200">%s</td>
				<td class="px-6 py-3 text-slate-300">%d</td>
				<td class="px-6 py-3 text-rose-400">%d</td>
			</tr>
		`, w.ID, statusBadge, current, w.Processed, w.Failed))
	}

	if len(snap.Workers) == 0 {
		workersRows.WriteString(`<tr><td colspan="5" class="px-6 py-6 text-center text-slate-500">No active workers running. Start with artisan queue:work</td></tr>`)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8"/>
	<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
	<title>GoLaravel Horizon - Queue & Worker Dashboard</title>
	<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-slate-900 text-slate-100 min-h-screen p-6 antialiased">
	<div class="max-w-6xl mx-auto space-y-8">
		<!-- Header -->
		<div class="flex items-center justify-between border-b border-slate-800 pb-5">
			<div>
				<div class="flex items-center space-x-3">
					<div class="h-3 w-3 rounded-full bg-emerald-500 animate-ping"></div>
					<h1 class="text-2xl font-bold tracking-tight text-white flex items-center gap-2">
						GoLaravel Horizon <span class="text-xs px-2 py-0.5 rounded bg-rose-500/20 text-rose-300 border border-rose-500/30">Dashboard</span>
					</h1>
				</div>
				<p class="text-sm text-slate-400 mt-1">Real-time queue monitoring, distributed workers, and dead-letter queue</p>
			</div>
			<div class="flex items-center space-x-3">
				<span class="text-xs font-mono text-slate-400">Uptime: <span class="text-white font-semibold">%s</span></span>
				<button onclick="location.reload()" class="px-3 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 text-xs font-medium rounded-lg border border-slate-700 transition">
					↻ Refresh
				</button>
			</div>
		</div>

		<!-- Overview Cards -->
		<div class="grid grid-cols-1 md:grid-cols-4 gap-4">
			<div class="bg-slate-800/80 border border-slate-700/80 p-5 rounded-xl shadow-lg">
				<p class="text-xs font-medium uppercase tracking-wider text-slate-400">Total Processed</p>
				<p class="text-3xl font-extrabold text-emerald-400 mt-2">%d</p>
			</div>
			<div class="bg-slate-800/80 border border-slate-700/80 p-5 rounded-xl shadow-lg">
				<p class="text-xs font-medium uppercase tracking-wider text-slate-400">Failed (DLQ)</p>
				<p class="text-3xl font-extrabold text-rose-400 mt-2" id="failedCountText">%d</p>
			</div>
			<div class="bg-slate-800/80 border border-slate-700/80 p-5 rounded-xl shadow-lg">
				<p class="text-xs font-medium uppercase tracking-wider text-slate-400">Active Workers</p>
				<p class="text-3xl font-extrabold text-sky-400 mt-2">%d</p>
			</div>
			<div class="bg-slate-800/80 border border-slate-700/80 p-5 rounded-xl shadow-lg">
				<p class="text-xs font-medium uppercase tracking-wider text-slate-400">Status</p>
				<p class="text-2xl font-bold text-slate-200 mt-2 flex items-center gap-2">
					<span class="h-2.5 w-2.5 rounded-full bg-emerald-400"></span> Active
				</p>
			</div>
		</div>

		<!-- Workers Table -->
		<div class="bg-slate-800/60 border border-slate-700/80 rounded-xl overflow-hidden shadow-xl">
			<div class="px-6 py-4 border-b border-slate-700/80 flex justify-between items-center bg-slate-800/80">
				<h2 class="text-base font-semibold text-white">Worker Pool Status</h2>
				<span class="text-xs text-slate-400">Concurrent Workers</span>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full text-left text-sm text-slate-300">
					<thead class="bg-slate-800/90 text-xs uppercase text-slate-400 border-b border-slate-700">
						<tr>
							<th class="px-6 py-3">Worker</th>
							<th class="px-6 py-3">Status</th>
							<th class="px-6 py-3">Current Execution</th>
							<th class="px-6 py-3">Jobs Completed</th>
							<th class="px-6 py-3">Failed</th>
						</tr>
					</thead>
					<tbody>
						%s
					</tbody>
				</table>
			</div>
		</div>

		<!-- Dead Letter Queue (DLQ) -->
		<div class="bg-slate-800/60 border border-slate-700/80 rounded-xl overflow-hidden shadow-xl">
			<div class="px-6 py-4 border-b border-slate-700/80 flex justify-between items-center bg-slate-800/80">
				<div>
					<h2 class="text-base font-semibold text-white">Failed Jobs (Dead-Letter Queue)</h2>
					<p class="text-xs text-slate-400">Jobs that exceeded maximum retry attempts</p>
				</div>
				<div class="space-x-2">
					<button onclick="retryAll()" class="px-3 py-1.5 bg-rose-600 hover:bg-rose-500 text-white text-xs font-semibold rounded-lg shadow transition">
						↻ Retry All
					</button>
					<button onclick="flushAll()" class="px-3 py-1.5 bg-slate-700 hover:bg-slate-600 text-slate-200 text-xs font-semibold rounded-lg transition">
						Flush All
					</button>
				</div>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full text-left text-sm text-slate-300" id="failedTable">
					<thead class="bg-slate-800/90 text-xs uppercase text-slate-400 border-b border-slate-700">
						<tr>
							<th class="px-6 py-3">ID</th>
							<th class="px-6 py-3">Queue</th>
							<th class="px-6 py-3">Job Type</th>
							<th class="px-6 py-3">Exception</th>
							<th class="px-6 py-3">Failed At</th>
							<th class="px-6 py-3 text-right">Actions</th>
						</tr>
					</thead>
					<tbody id="failedTableBody">
						<tr><td colspan="6" class="px-6 py-4 text-center text-slate-500">Loading failed jobs...</td></tr>
					</tbody>
				</table>
			</div>
		</div>
	</div>

	<script>
		async function loadFailedJobs() {
			try {
				const res = await fetch('%s/api/failed');
				const jobs = await res.json();
				const tbody = document.getElementById('failedTableBody');
				if (!jobs || jobs.length === 0) {
					tbody.innerHTML = '<tr><td colspan="6" class="px-6 py-6 text-center text-slate-500">🎉 Dead-Letter Queue is empty! No failed jobs.</td></tr>';
					return;
				}
				tbody.innerHTML = jobs.map(function(j) {
					return '<tr class="hover:bg-slate-700/30 border-b border-slate-700/40">' +
						'<td class="px-6 py-3 font-mono text-xs text-slate-400">' + j.id + '</td>' +
						'<td class="px-6 py-3"><span class="px-2 py-0.5 rounded text-xs bg-slate-700 text-slate-300">' + j.queue + '</span></td>' +
						'<td class="px-6 py-3 font-medium text-white">' + j.type + '</td>' +
						'<td class="px-6 py-3 text-xs text-rose-300 font-mono max-w-xs truncate">' + j.exception + '</td>' +
						'<td class="px-6 py-3 text-xs text-slate-400">' + new Date(j.failed_at).toLocaleTimeString() + '</td>' +
						'<td class="px-6 py-3 text-right space-x-2">' +
							'<button onclick="retryJob(\'' + j.id + '\')" class="text-xs text-emerald-400 hover:underline">Retry</button> ' +
							'<button onclick="deleteJob(\'' + j.id + '\')" class="text-xs text-rose-400 hover:underline">Delete</button>' +
						'</td>' +
					'</tr>';
				}).join('');
			} catch(e) {
				console.error(e);
			}
		}

		async function retryJob(id) {
			await fetch('%s/api/retry/' + id, { method: 'POST' });
			loadFailedJobs();
		}

		async function deleteJob(id) {
			await fetch('%s/api/failed/' + id, { method: 'DELETE' });
			loadFailedJobs();
		}

		async function retryAll() {
			if (!confirm('Retry all failed jobs?')) return;
			await fetch('%s/api/retry-all', { method: 'POST' });
			loadFailedJobs();
		}

		async function flushAll() {
			if (!confirm('Flush all failed jobs permanently?')) return;
			await fetch('%s/api/flush', { method: 'POST' });
			loadFailedJobs();
		}

		loadFailedJobs();
	</script>
</body>
</html>`, snap.Uptime, snap.TotalProcessed, failedCount, snap.ActiveWorkers, workersRows.String(), base, base, base, base, base)
}
