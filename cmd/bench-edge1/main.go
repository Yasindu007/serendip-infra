package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "render" {
		if err := runRender(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bench-edge1 render:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "compare" {
		if err := runCompare(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "bench-edge1 compare:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bench-edge1:", err)
		os.Exit(1)
	}
}

type options struct {
	serendipd, kubeconfig, out, addr, image string
	settle, interval                        time.Duration
	samples, components, pollClients        int
	sim                                     Simulation
	notSimulated                            string
	// pkg, when its provider is set, replaces the light-workload phases
	// with a package run (specs/022-builds T013).
	pkg           packageOptions
	kubeconfigAbs string
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("bench-edge1", flag.ContinueOnError)
	fs.StringVar(&o.serendipd, "serendipd", "", "path to the serendipd binary to benchmark (required)")
	fs.StringVar(&o.kubeconfig, "kubeconfig", "", "kubeconfig of the edge-1 cluster, passed to serendipd as SERENDIP_KUBECONFIG (required)")
	fs.StringVar(&o.out, "out", "bench-out", "output directory for result.json, summary.md, and serendipd's log and database")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:18090", "address serendipd listens on during the run")
	fs.StringVar(&o.image, "image", "nginx:1.27-alpine", "image for the light-workload Components")
	fs.DurationVar(&o.settle, "settle", 3*time.Minute, "wait after everything is ready before sampling")
	fs.DurationVar(&o.interval, "interval", 10*time.Second, "time between samples")
	fs.IntVar(&o.samples, "samples", 12, "samples per phase")
	fs.IntVar(&o.components, "components", 3, "light-workload Components to deploy (exposed through the gateway)")
	fs.IntVar(&o.pollClients, "poll-clients", 3, "simulated web-console tabs polling a Component page during the polling phase (0 skips it)")
	fs.Float64Var(&o.sim.CPUs, "sim-cpus", 0, "CPU quota applied to simulate reference hardware (marks the run as a simulation)")
	fs.StringVar(&o.sim.CPUSet, "sim-cpuset", "", "cpuset applied to simulate reference hardware (marks the run as a simulation)")
	fs.StringVar(&o.sim.Note, "sim-note", "", "free-text description of the simulation")
	fs.StringVar(&o.notSimulated, "not-simulated", "", "semicolon-separated reference-hardware traits the simulation could not reproduce")
	fs.StringVar(&o.pkg.provider, "package", "", "measure this optional package instead of the light workload (a provider name from GET /v1/packages)")
	fs.StringVar(&o.pkg.repo, "package-repo", "https://github.com/openchoreo/sample-workloads", "builds package: the public repository the sample builds come from")
	fs.StringVar(&o.pkg.branch, "package-branch", "main", "builds package: the sample repository's branch")
	fs.StringVar(&o.pkg.path, "package-path", "service-go-greeter", "builds package: the build context in the sample repository")
	fs.IntVar(&o.pkg.idleSamples, "package-idle-samples", 60, "package idle samples (60 at 10 s is 10 minutes)")
	fs.DurationVar(&o.pkg.idleInterval, "package-idle-interval", 10*time.Second, "time between package idle and auto-build samples")
	fs.DurationVar(&o.pkg.buildInterval, "package-build-interval", 3*time.Second, "time between samples while a build runs")
	fs.IntVar(&o.pkg.pollerSamples, "package-poller-samples", 33, "samples with auto-build on (33 at 10 s spans one 5-minute poll; 0 skips it)")
	fs.BoolVar(&o.pkg.fullHelm, "package-full-helm", true, "run the package's full Helm command once, for comparison with the fingerprint path")
	fs.DurationVar(&o.pkg.installTimeout, "package-install-timeout", 20*time.Minute, "how long the package may take to become Ready")
	fs.DurationVar(&o.pkg.buildTimeout, "package-build-timeout", 20*time.Minute, "how long one build may take")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.serendipd == "" || o.kubeconfig == "" {
		return o, fmt.Errorf("--serendipd and --kubeconfig are required")
	}
	for _, s := range strings.Split(o.notSimulated, ";") {
		if s = strings.TrimSpace(s); s != "" {
			o.sim.NotSimulated = append(o.sim.NotSimulated, s)
		}
	}
	return o, nil
}

func run(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return err
	}
	abs := func(p string) string { a, _ := filepath.Abs(p); return a }
	kubeconfig := abs(o.kubeconfig)
	o.kubeconfigAbs = kubeconfig
	dbPath := filepath.Join(abs(o.out), "serendipd.db")
	credsPath := filepath.Join(abs(o.out), "openbao-init.json")
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", credsPath} {
		_ = os.Remove(p)
	}

	cl, err := newCluster(kubeconfig)
	if err != nil {
		return err
	}
	node, err := cl.node(ctx)
	if err != nil {
		return err
	}

	res := Result{
		StartedAt:      time.Now(),
		Simulation:     o.sim,
		SettleSeconds:  o.settle.Seconds(),
		SampleInterval: o.interval.Seconds(),
		Components:     o.components,
		Machine: MachineProfile{
			HostOS: runtime.GOOS + "/" + runtime.GOARCH, HostCPU: hostCPUModel(),
			NodeName: node.Name, NodeKernel: node.Kernel, NodeOSImage: node.OSImage,
			ContainerRuntime: node.Runtime, KubeletVersion: node.Kubelet,
			NodeCPUCapacity: node.CPUCap, NodeMemoryCapacity: node.MemoryCap,
		},
	}

	d := &daemon{
		bin:  abs(o.serendipd),
		log:  filepath.Join(abs(o.out), "serendipd.log"),
		addr: o.addr,
		env: []string{
			"SERENDIP_KUBECONFIG=" + kubeconfig,
			"SERENDIP_DB_PATH=" + dbPath,
			"SERENDIP_ADDR=" + o.addr,
			"SERENDIP_API_KEYS=" + apiKey + ":bench",
			"SERENDIP_OPENBAO_CREDENTIALS_PATH=" + credsPath,
		},
	}
	defer d.stop()
	a := api{base: "http://" + o.addr, http: &http.Client{Timeout: 30 * time.Second}}

	log.Printf("cold start: launching serendipd (installs the whole stack)")
	cold, err := d.start(ctx, 45*time.Minute)
	if err != nil {
		return fmt.Errorf("cold start: %w", err)
	}
	res.Timings.ColdStart = Duration(cold)
	log.Printf("cold start: %s", cold.Round(time.Second))

	// From here on, whatever was measured is written even if a later phase
	// fails, and the light workload is always removed so the next run on
	// this cluster isn't skewed by leftovers.
	var w workload
	measureErr := measure(ctx, o, cl, node.Name, d, a, &res, &w)
	if measureErr != nil {
		res.Findings = append(res.Findings, "run aborted: "+measureErr.Error())
	}
	if w.project != "" {
		if err := a.deleteWorkload(context.Background(), w); err != nil {
			res.Findings = append(res.Findings, "deleting the light workload failed: "+err.Error())
		}
	}

	if measureErr == nil {
		log.Printf("warm restart: restarting serendipd against the installed stack")
		d.stop()
		warm, err := d.start(ctx, 20*time.Minute)
		if err != nil {
			res.Findings = append(res.Findings, "warm restart failed: "+err.Error())
		} else {
			res.Timings.WarmRestart = Duration(warm)
			log.Printf("warm restart: %s", warm.Round(time.Second))
		}
	}
	d.stop()

	capacity := o.sim.CPUs
	if capacity == 0 {
		capacity = parseCPUQuantity(node.CPUCap)
	}
	res.Rule4 = checkRule4(res.Serendipd, capacity)
	if res.Polling != nil {
		res.Polling.check(capacity)
	}
	if len(res.WorkloadSamples) > 0 {
		platformMiB := res.Platform.NodeMemoryMiB.Mean + res.Serendipd.RSSMiB.Mean
		res.Headroom = headroom(platformMiB, workloadDeltaMiB(res.Platform, res.Workload, o.components))
	} else {
		res.Headroom = Headroom{Verdict: "not computed: no light-workload data"}
	}
	res.Findings = append(res.Findings, autoFindings(res)...)

	if err := writeResult(o.out, res); err != nil {
		return err
	}
	return measureErr
}

// measure runs the platform-idle and light-workload phases, filling res as
// it goes so a failure part-way leaves everything measured so far.
func measure(ctx context.Context, o options, cl *cluster, nodeName string, d *daemon, a api, res *Result, created *workload) error {
	if err := cl.waitAllPodsReady(ctx, 15*time.Minute); err != nil {
		return err
	}
	log.Printf("platform: all pods ready, settling %s", o.settle)
	if err := sleep(ctx, o.settle); err != nil {
		return err
	}
	platformSamples, procSamples, err := samplePhase(ctx, cl, nodeName, d, o.samples, o.interval)
	res.PlatformSamples = platformSamples
	res.Platform = aggregate(platformSamples)
	res.Serendipd = processStatsOf(procSamples)
	if err != nil {
		return fmt.Errorf("platform sampling: %w", err)
	}
	if o.pkg.provider != "" {
		return measurePackage(ctx, o, cl, nodeName, d, a, res, created)
	}

	log.Printf("workload: deploying %d exposed Components", o.components)
	w, serving, err := a.deployWorkload(ctx, o.components, o.image)
	*created = w
	res.Timings.TimeToServing = serving
	if err != nil {
		res.Findings = append(res.Findings, "light workload did not reach Serving: "+err.Error())
		return nil
	}
	if err := cl.waitAllPodsReady(ctx, 10*time.Minute); err != nil {
		return err
	}
	log.Printf("workload: serving, settling %s", o.settle)
	if err := sleep(ctx, o.settle); err != nil {
		return err
	}
	workloadSamples, workloadProcs, err := samplePhase(ctx, cl, nodeName, d, o.samples, o.interval)
	if err != nil {
		return fmt.Errorf("workload sampling: %w", err)
	}
	res.WorkloadSamples = workloadSamples
	res.Workload = aggregate(workloadSamples)
	res.SerendipdWorkload = processStatsOf(workloadProcs)

	if o.pollClients <= 0 {
		return nil
	}
	comps, err := a.componentIDs(ctx, w.app)
	if err != nil {
		res.Findings = append(res.Findings, "console polling phase skipped: "+err.Error())
		return nil
	}
	if len(comps) == 0 {
		res.Findings = append(res.Findings, "console polling phase skipped: no components to poll")
		return nil
	}
	log.Printf("polling: %d console tabs on a Component page", o.pollClients)
	st, stop := a.startConsoleTabs(ctx, w.app, comps, o.pollClients)
	start := time.Now()
	_, pollProcs, err := samplePhase(ctx, cl, nodeName, d, o.samples, o.interval)
	stop()
	elapsed := time.Since(start).Seconds()
	res.Polling = &Polling{
		Clients:           o.pollClients,
		Requests:          st.requests.Load(),
		Errors:            st.errors.Load(),
		RequestsPerSecond: float64(st.requests.Load()) / elapsed,
		Serendipd:         processStatsOf(pollProcs),
	}
	if err != nil {
		return fmt.Errorf("polling sampling: %w", err)
	}
	return nil
}

// samplePhase takes n readings, interval apart, of the cluster and of
// serendipd's host process. serendipd exiting mid-phase is an error, not a
// silently empty process series (which would read as zero usage).
func samplePhase(ctx context.Context, cl *cluster, nodeName string, d *daemon, n int, interval time.Duration) ([]Sample, []procSample, error) {
	var samples []Sample
	var procs []procSample
	for i := 0; i < n; i++ {
		if i > 0 {
			if err := sleep(ctx, interval); err != nil {
				return samples, procs, err
			}
		}
		select {
		case <-d.done:
			return samples, procs, fmt.Errorf("serendipd exited during sampling (%v)", d.exitErr)
		default:
		}
		s, err := cl.summary(ctx, nodeName)
		if err != nil {
			return samples, procs, err
		}
		samples = append(samples, sampleFrom(s))
		p, err := readProcess(d.pid())
		if err != nil {
			return samples, procs, fmt.Errorf("read serendipd process: %w", err)
		}
		procs = append(procs, p)
		log.Printf("  sample %d/%d: node %.0f MiB, %.0f mcores", i+1, n, mib(samples[i].NodeWorkingSetBytes), millicores(samples[i].NodeNanoCores))
	}
	return samples, procs, nil
}

func processStatsOf(ps []procSample) ProcessStats {
	var rss, cpu []float64
	for i, p := range ps {
		rss = append(rss, mib(p.RSS))
		if i > 0 {
			cpu = append(cpu, cpuMillicores(ps[i-1], p))
		}
	}
	return ProcessStats{RSSMiB: statOf(rss), CPUMillicores: statOf(cpu)}
}

// autoFindings records facts a reader of the numbers should not miss.
func autoFindings(r Result) []string {
	var out []string
	for g := range r.Platform.Groups {
		if strings.HasPrefix(g, groupOtherPrefix) {
			out = append(out, fmt.Sprintf("unattributed namespace in the platform sample: %s", strings.TrimPrefix(g, groupOtherPrefix)))
		}
	}
	switch {
	case r.Rule4.RSSMiB == 0:
		out = append(out, "rule 4 not evaluated: serendipd was not measured")
	case !r.Rule4.RSSPass || !r.Rule4.CPUPass:
		out = append(out, "serendipd exceeds the rule-4 control-plane budget")
	}
	if r.Platform.NodeOverheadMiB.Mean < 0 {
		out = append(out, fmt.Sprintf("node overhead came out negative (%.0f MiB): pod and node memory accounting disagree on this node, so treat the overhead row as unreliable", r.Platform.NodeOverheadMiB.Mean))
	}
	return out
}

func writeResult(dir string, r Result) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), raw, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.md"), []byte(renderSummary(r)), 0o644); err != nil {
		return err
	}
	log.Printf("wrote %s and %s", filepath.Join(dir, "result.json"), filepath.Join(dir, "summary.md"))
	return nil
}

func runCompare(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: bench-edge1 compare <run1/result.json> <run2/result.json>")
	}
	var rs [2]Result
	for i, p := range args {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &rs[i]); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	fmt.Print(renderComparison(compareRuns(rs[0], rs[1])))
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// runRender rewrites summary.md next to a result.json, so a summary can be
// regenerated after a report-format change without re-running the
// benchmark.
func runRender(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: bench-edge1 render <run/result.json>")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var r Result
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	// A package's stated cost is derived from the samples alone, so it's
	// recomputed: a run from before a change to packageCost then states
	// its cost the same way as a new one, in result.json too.
	if r.Package != nil {
		r.Package.Cost = packageCost(*r.Package, r.Platform)
		return writeResult(filepath.Dir(args[0]), r)
	}
	out := filepath.Join(filepath.Dir(args[0]), "summary.md")
	return os.WriteFile(out, []byte(renderSummary(r)), 0o644)
}
