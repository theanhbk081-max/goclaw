package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ContainerEngine runs Chrome in a Docker container per session.
// It delegates all Page/Engine operations to an inner ChromeEngine connected via CDP.
type ContainerEngine struct {
	image       string // e.g. "zenika/alpine-chrome:latest"
	containerID string
	cdpPort     int
	inner       Engine // ChromeEngine connected to the container
	mu          sync.Mutex
	logger      *slog.Logger

	// Resource limits (set via ContainerOpt)
	memoryMB int     // --memory flag (MB), 0 = no limit
	cpuLimit float64 // --cpus flag, 0 = no limit
	network  string  // --network flag, "" = default
}

// ContainerOpt configures a ContainerEngine.
type ContainerOpt func(*ContainerEngine)

// WithContainerMemory sets the memory limit per container in MB.
func WithContainerMemory(mb int) ContainerOpt {
	return func(e *ContainerEngine) { e.memoryMB = mb }
}

// WithContainerCPU sets the CPU core limit per container.
func WithContainerCPU(cpu float64) ContainerOpt {
	return func(e *ContainerEngine) { e.cpuLimit = cpu }
}

// WithContainerNetwork sets the Docker network name for the container.
func WithContainerNetwork(network string) ContainerOpt {
	return func(e *ContainerEngine) { e.network = network }
}

// DefaultContainerImage is the default Docker image for container engine.
// chromedp/headless-shell binds to 0.0.0.0 out of the box and supports screencast
// since Chrome 132+ (headless-shell IS new headless, old headless was removed).
const DefaultContainerImage = "chromedp/headless-shell:latest"

// NewContainerEngine creates a ContainerEngine that will use the given Docker image.
func NewContainerEngine(image string, logger *slog.Logger, opts ...ContainerOpt) *ContainerEngine {
	if logger == nil {
		logger = slog.Default()
	}
	if image == "" {
		image = DefaultContainerImage
	}
	e := &ContainerEngine{
		image:  image,
		logger: logger,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

func (e *ContainerEngine) Launch(opts LaunchOpts) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// If container is still running, try to reconnect instead of recreating
	if e.containerID != "" && e.cdpPort > 0 && isContainerRunning(e.containerID) {
		if e.inner != nil {
			e.inner.Close()
		}
		inner := NewChromeEngine(e.logger)
		if err := inner.Launch(LaunchOpts{
			RemoteURL: fmt.Sprintf("ws://127.0.0.1:%d", e.cdpPort),
		}); err == nil {
			e.inner = inner
			e.logger.Info("reconnected to existing container", "id", e.containerID[:min(12, len(e.containerID))], "port", e.cdpPort)
			return nil
		}
		e.logger.Warn("existing container unreachable, recreating")
	}

	// Cleanup any previous container
	if e.inner != nil {
		e.inner.Close()
		e.inner = nil
	}
	e.stopContainer()

	// Find a free port
	port, err := freePort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	e.cdpPort = port

	// Build docker run command.
	// chromedp/headless-shell: has its own run.sh that binds to 0.0.0.0:9222.
	//   No Chrome flags needed — just pass the image name.
	// zenika/alpine-chrome: entrypoint = ["chromium-browser","--headless"] which
	//   binds to 127.0.0.1 only. Override entrypoint to bind to 0.0.0.0.
	// Other images: pass full Chrome flags for maximum compat.
	isHeadlessShell := strings.Contains(e.image, "headless-shell")
	isZenika := strings.Contains(e.image, "zenika/alpine-chrome")

	args := []string{
		"run", "-d", "--rm",
		"-p", fmt.Sprintf("%d:9222", port),
		"--shm-size=256m", // Chrome needs shared memory for rendering
	}

	// Resource limits
	if e.memoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", e.memoryMB))
	}
	if e.cpuLimit > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", e.cpuLimit))
	}
	if e.network != "" {
		args = append(args, "--network", e.network)
	}

	// Volume mount for profile persistence
	if opts.ProfileDir != "" {
		args = append(args, "-v", opts.ProfileDir+":/data/profile")
		args = append(args, "--label", "goclaw.profile="+opts.ProfileDir)
	}

	// Pass proxy if configured
	if opts.ProxyURL != "" {
		args = append(args, "-e", "CHROME_PROXY="+opts.ProxyURL)
	}

	// Chrome user-data-dir flag when profile is mounted
	userDataDirFlag := ""
	if opts.ProfileDir != "" {
		userDataDirFlag = "--user-data-dir=/data/profile"
	}

	// Stealth flags for container Chrome — same as StealthFlags() for local launcher.
	// Without these, Chrome in Docker runs with full automation signals exposed.
	containerStealthFlags := []string{
		"--disable-blink-features=AutomationControlled",
		"--disable-features=AutomationControlled,TranslateUI,EnableAutomation",
		"--disable-infobars",
		"--disable-background-networking",
		"--disable-client-side-phishing-detection",
		"--disable-default-apps",
		"--disable-hang-monitor",
		"--disable-popup-blocking",
		"--disable-prompt-on-repost",
		"--disable-sync",
		"--disable-extensions",
		"--disable-component-extensions-with-background-pages",
		"--metrics-recording-only",
		"--no-first-run",
		"--password-store=basic",
		"--use-mock-keychain",
		"--enforce-webrtc-ip-permission-check",
		"--disable-webrtc-hw-decoding",
		"--disable-webrtc-hw-encoding",
		"--window-size=1920,1080",
	}

	switch {
	case isHeadlessShell:
		// chromedp/headless-shell: run.sh passes $@ to the binary, so we can
		// append stealth flags without overriding the entrypoint.
		args = append(args, e.image)
		args = append(args, containerStealthFlags...)
		if userDataDirFlag != "" {
			args = append(args, userDataDirFlag)
		}
	case isZenika:
		// Override entrypoint to bind debugger to 0.0.0.0
		chromeArgs := []string{
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--disable-gpu",
			"--headless=new",
			"--remote-debugging-address=0.0.0.0",
			"--remote-debugging-port=9222",
		}
		chromeArgs = append(chromeArgs, containerStealthFlags...)
		if userDataDirFlag != "" {
			chromeArgs = append(chromeArgs, userDataDirFlag)
		}
		args = append(args, "--entrypoint", "chromium-browser", e.image)
		args = append(args, chromeArgs...)
	default:
		// Generic Chrome/Chromium image: pass all flags explicitly
		chromeArgs := []string{
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--disable-gpu",
			"--headless=new",
			"--remote-debugging-address=0.0.0.0",
			"--remote-debugging-port=9222",
		}
		chromeArgs = append(chromeArgs, containerStealthFlags...)
		if userDataDirFlag != "" {
			chromeArgs = append(chromeArgs, userDataDirFlag)
		}
		args = append(args, e.image)
		args = append(args, chromeArgs...)
	}
	if opts.ProxyURL != "" {
		args = append(args, "--proxy-server="+opts.ProxyURL)
	}

	// Start container
	cmd := exec.Command("docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	e.containerID = strings.TrimSpace(string(out))
	if len(e.containerID) > 12 {
		e.logger.Info("container started", "id", e.containerID[:12], "port", port, "image", e.image)
	}

	// Wait for CDP to be ready
	cdpURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForCDP(cdpURL, 30*time.Second, e.logger); err != nil {
		// Grab container logs for debugging before cleanup
		if logOut, logErr := exec.Command("docker", "logs", "--tail", "20", e.containerID).CombinedOutput(); logErr == nil {
			e.logger.Warn("container logs before cleanup", "logs", strings.TrimSpace(string(logOut)))
		}
		e.stopContainer()
		return fmt.Errorf("wait for CDP: %w", err)
	}

	// Create inner ChromeEngine connected via remote CDP
	inner := NewChromeEngine(e.logger)
	if err := inner.Launch(LaunchOpts{
		RemoteURL: fmt.Sprintf("ws://127.0.0.1:%d", port),
	}); err != nil {
		e.stopContainer()
		return fmt.Errorf("connect to container Chrome: %w", err)
	}
	e.inner = inner
	return nil
}

func (e *ContainerEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var innerErr error
	if e.inner != nil {
		innerErr = e.inner.Close()
		e.inner = nil
	}
	e.stopContainer()
	return innerErr
}

// isContainerRunning checks if a Docker container is still running.
func isContainerRunning(containerID string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", containerID).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func (e *ContainerEngine) stopContainer() {
	if e.containerID == "" {
		return
	}
	id := e.containerID
	if len(id) > 12 {
		id = id[:12]
	}
	// Use a timeout to prevent docker rm -f from blocking shutdown indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", e.containerID)
	if err := cmd.Run(); err != nil {
		e.logger.Warn("failed to remove container", "id", id, "error", err)
	} else {
		e.logger.Info("container removed", "id", id)
	}
	e.containerID = ""
}

func (e *ContainerEngine) NewPage(ctx context.Context, url string) (Page, error) {
	if e.inner == nil {
		return nil, fmt.Errorf("container engine not running")
	}
	return e.inner.NewPage(ctx, url)
}

func (e *ContainerEngine) Pages() ([]Page, error) {
	if e.inner == nil {
		return nil, fmt.Errorf("container engine not running")
	}
	return e.inner.Pages()
}

func (e *ContainerEngine) Incognito() (Engine, error) {
	if e.inner == nil {
		return nil, fmt.Errorf("container engine not running")
	}
	return e.inner.Incognito()
}

func (e *ContainerEngine) IsConnected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.inner != nil && e.inner.IsConnected() {
		return true
	}

	// Inner connection lost but container may still be running — try to reconnect
	if e.containerID != "" && e.cdpPort > 0 {
		if isContainerRunning(e.containerID) {
			e.logger.Info("container still running, reconnecting inner engine", "port", e.cdpPort)
			if e.inner != nil {
				e.inner.Close()
			}
			inner := NewChromeEngine(e.logger)
			if err := inner.Launch(LaunchOpts{
				RemoteURL: fmt.Sprintf("ws://127.0.0.1:%d", e.cdpPort),
			}); err == nil {
				e.inner = inner
				return true
			}
			e.logger.Warn("reconnect to container failed", "port", e.cdpPort)
		}
	}
	return false
}

func (e *ContainerEngine) Name() string { return "container" }

// freePort finds an available TCP port.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// waitForCDP polls the Chrome /json/version endpoint until ready or timeout.
func waitForCDP(baseURL string, timeout time.Duration, logger *slog.Logger) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	url := baseURL + "/json/version"

	var lastErr error
	attempts := 0
	for time.Now().Before(deadline) {
		attempts++
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if result["webSocketDebuggerUrl"] != nil {
			if logger != nil {
				logger.Info("CDP ready", "url", baseURL, "attempts", attempts)
			}
			return nil
		}
		lastErr = fmt.Errorf("missing webSocketDebuggerUrl in response")
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("CDP not ready after %s (%d attempts, last error: %v)", timeout, attempts, lastErr)
}
