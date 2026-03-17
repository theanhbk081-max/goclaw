// pkg-helper is a root-privileged helper that listens on a Unix socket
// and executes apk add/del commands on behalf of the non-root app process.
// It is started by docker-entrypoint.sh before dropping privileges.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
)

const socketPath = "/tmp/pkg.sock"

// validPkgName allows alphanumeric, hyphens, underscores, dots, @, / (scoped npm).
// Rejects names starting with - to prevent argument injection.
var validPkgName = regexp.MustCompile(`^[a-zA-Z0-9@][a-zA-Z0-9._+\-/@]*$`)

type request struct {
	Action  string `json:"action"`
	Package string `json:"package"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func main() {
	slog.Info("pkg-helper: starting", "socket", socketPath)

	// Remove stale socket.
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		slog.Error("pkg-helper: listen failed", "error", err)
		os.Exit(1)
	}
	defer listener.Close()

	// Set socket permissions: owner root, group goclaw (gid 1000), mode 0660.
	if err := os.Chown(socketPath, 0, 1000); err != nil {
		slog.Warn("pkg-helper: chown socket failed", "error", err)
	}
	if err := os.Chmod(socketPath, 0660); err != nil {
		slog.Warn("pkg-helper: chmod socket failed", "error", err)
	}

	// Graceful shutdown on SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		slog.Info("pkg-helper: shutting down")
		listener.Close()
		os.Remove(socketPath)
		os.Exit(0)
	}()

	slog.Info("pkg-helper: ready")

	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener closed — exit.
			break
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)

	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			encoder.Encode(response{Error: "invalid json"}) //nolint:errcheck
			continue
		}

		resp := handleRequest(req)
		encoder.Encode(resp) //nolint:errcheck
	}
}

func handleRequest(req request) response {
	pkg := req.Package
	if pkg == "" {
		return response{Error: "package required"}
	}
	if !validPkgName.MatchString(pkg) {
		return response{Error: "invalid package name"}
	}

	switch req.Action {
	case "install":
		return doInstall(pkg)
	case "uninstall":
		return doUninstall(pkg)
	default:
		return response{Error: fmt.Sprintf("unknown action: %s", req.Action)}
	}
}

func doInstall(pkg string) response {
	slog.Info("pkg-helper: installing", "package", pkg)

	cmd := exec.Command("apk", "add", "--no-cache", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("%s: %v", strings.TrimSpace(string(out)), err)
		slog.Error("pkg-helper: install failed", "package", pkg, "error", msg)
		return response{Error: msg}
	}

	persistAdd(pkg)
	slog.Info("pkg-helper: installed", "package", pkg)
	return response{OK: true}
}

func doUninstall(pkg string) response {
	slog.Info("pkg-helper: uninstalling", "package", pkg)

	cmd := exec.Command("apk", "del", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := fmt.Sprintf("%s: %v", strings.TrimSpace(string(out)), err)
		slog.Error("pkg-helper: uninstall failed", "package", pkg, "error", msg)
		return response{Error: msg}
	}

	persistRemove(pkg)
	slog.Info("pkg-helper: uninstalled", "package", pkg)
	return response{OK: true}
}

// persistAdd appends a package name to the apk persist file.
func persistAdd(pkg string) {
	listFile := apkListFile()
	f, err := os.OpenFile(listFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Warn("pkg-helper: persist add failed", "error", err)
		return
	}
	defer f.Close()
	fmt.Fprintln(f, pkg)
}

// persistRemove removes a package name from the apk persist file.
func persistRemove(pkg string) {
	listFile := apkListFile()
	data, err := os.ReadFile(listFile)
	if err != nil {
		return
	}

	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != pkg {
			kept = append(kept, line)
		}
	}

	os.WriteFile(listFile, []byte(strings.Join(kept, "\n")+"\n"), 0644) //nolint:errcheck
}

func apkListFile() string {
	runtimeDir := os.Getenv("RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = "/app/data/.runtime"
	}
	return runtimeDir + "/apk-packages"
}
