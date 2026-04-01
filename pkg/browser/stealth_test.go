package browser

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestStealthFingerprint checks detection test sites for automation signals.
// Uses bot.sannysoft.com which tests all common detection vectors.
//
//	go test -v -run TestStealthFingerprint -timeout 60s ./pkg/browser/
func TestStealthFingerprint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stealth integration test in short mode")
	}

	logger := slog.Default()
	eng := NewContainerEngine("", logger)

	err := eng.Launch(LaunchOpts{})
	if err != nil {
		t.Fatalf("launch container: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	page, err := eng.NewPage(ctx, "https://bot.sannysoft.com/")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	// Wait for detection tests to complete
	waitDone := make(chan error, 1)
	go func() { waitDone <- page.WaitStable(2 * time.Second) }()
	select {
	case <-waitDone:
	case <-time.After(15 * time.Second):
	}

	// Check key detection properties
	checks := []struct {
		name   string
		js     string
		expect string
	}{
		{"webdriver", "() => String(navigator.webdriver)", "undefined"},
		{"chrome", "() => typeof window.chrome", "object"},
		{"chrome.runtime", "() => typeof window.chrome.runtime", "object"},
		{"chrome.csi", "() => typeof window.chrome.csi", "function"},
		{"chrome.loadTimes", "() => typeof window.chrome.loadTimes", "function"},
		{"plugins.length", "() => String(navigator.plugins.length > 0)", "true"},
		{"languages", "() => String(navigator.languages.length > 0)", "true"},
		{"userAgent no headless", "() => /HeadlessChrome/.test(navigator.userAgent) ? 'HEADLESS_LEAK' : 'ok'", "ok"},
		{"connection", "() => typeof navigator.connection", "object"},
		{"permissions", "() => typeof navigator.permissions.query", "function"},
		// PHANTOM_WINDOW_HEIGHT: outerHeight must match screen.height
		{"outerWidth matches screen", "() => String(window.outerWidth === screen.width)", "true"},
		{"outerHeight matches screen", "() => String(window.outerHeight === screen.height)", "true"},
		{"screen.availWidth", "() => String(screen.availWidth > 0)", "true"},
		{"screen.availHeight", "() => String(screen.availHeight > 0)", "true"},
	}

	for _, c := range checks {
		res, err := page.Eval(c.js)
		if err != nil {
			t.Logf("  [WARN] %s: %v", c.name, err)
			continue
		}
		val := fmt.Sprintf("%v", res.Value)
		if val != c.expect {
			t.Errorf("  [FAIL] %s = %q, want %q", c.name, val, c.expect)
		} else {
			t.Logf("  [OK] %s = %s", c.name, val)
		}
	}
}

// TestStealthGoogleSearch verifies that the browser can perform a Google search
// without triggering bot detection. Run with:
//
//	go test -v -run TestStealthGoogleSearch -timeout 120s ./pkg/browser/
//
// NOTE: This test is sensitive to IP rate-limiting. If your IP has been flagged
// by Google (e.g., from repeated automated requests), it will fail regardless
// of stealth quality. Wait a few hours or use a different IP to get a clean test.
func TestStealthGoogleSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stealth integration test in short mode")
	}

	logger := slog.Default()
	eng := NewContainerEngine("", logger)

	err := eng.Launch(LaunchOpts{})
	if err != nil {
		t.Fatalf("launch container: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Step 1: Visit Google homepage first to establish cookies (like a real user)
	page, err := eng.NewPage(ctx, "https://www.google.com/")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	waitStable(page, 2*time.Second, 5*time.Second)

	homeInfo, _ := page.Info()
	t.Logf("Homepage: %s — %s", homeInfo.URL, homeInfo.Title)

	// Check homepage is accessible
	homeCheck, _ := page.Eval("() => document.body.innerText.includes('unusual traffic') ? 'blocked' : 'ok'")
	if fmt.Sprintf("%v", homeCheck.Value) == "blocked" {
		t.Skip("IP rate-limited by Google — homepage blocked, skip search test")
	}

	// Step 2: Simulate human delay before searching
	time.Sleep(3 * time.Second)

	// Step 3: Navigate to search
	if err := page.Navigate("https://www.google.com/search?q=test"); err != nil {
		t.Fatalf("navigate to search: %v", err)
	}

	waitStable(page, 500*time.Millisecond, 10*time.Second)

	info, err := page.Info()
	if err != nil {
		t.Fatalf("page info: %v", err)
	}

	t.Logf("Search URL: %s", info.URL)
	t.Logf("Search Title: %s", info.Title)

	// Check results
	captcha, _ := page.Eval("() => document.querySelector('iframe[src*=\"recaptcha\"]') ? 'captcha' : 'none'")
	results, _ := page.Eval("() => document.querySelectorAll('#search .g, #rso .g, div[data-hveid]').length")

	captchaVal := fmt.Sprintf("%v", captcha.Value)
	resultsVal := fmt.Sprintf("%v", results.Value)

	t.Logf("CAPTCHA: %s", captchaVal)
	t.Logf("Search results: %s", resultsVal)

	if strings.Contains(captchaVal, "captcha") {
		if strings.Contains(info.URL, "/sorry/") {
			t.Error("Google CAPTCHA triggered — likely IP rate-limited from repeated tests. Wait and retry with a fresh IP.")
		} else {
			t.Error("Google bot detection triggered — CAPTCHA detected")
		}
	}
}

// TestStealthHeadlessDetection tests against intoli.com headless detection page.
//
//	go test -v -run TestStealthHeadlessDetection -timeout 60s ./pkg/browser/
func TestStealthHeadlessDetection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stealth integration test in short mode")
	}

	logger := slog.Default()
	eng := NewContainerEngine("", logger)

	err := eng.Launch(LaunchOpts{})
	if err != nil {
		t.Fatalf("launch container: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	page, err := eng.NewPage(ctx, "https://arh.antoinevastel.com/bots/areyouheadless")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	waitStable(page, 2*time.Second, 10*time.Second)

	info, _ := page.Info()
	t.Logf("URL: %s", info.URL)

	// This page shows "You are not Chrome headless" if undetected
	result, err := page.Eval("() => document.querySelector('#res')?.innerText || document.body.innerText.substring(0, 200)")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	val := fmt.Sprintf("%v", result.Value)
	t.Logf("Detection result: %s", val)

	if strings.Contains(strings.ToLower(val), "you are chrome headless") ||
		strings.Contains(strings.ToLower(val), "headless: true") {
		t.Error("Headless Chrome detected!")
	}
}

// TestStealthNowsecure tests against nowsecure.nl anti-bot detection.
//
//	go test -v -run TestStealthNowsecure -timeout 60s ./pkg/browser/
func TestStealthNowsecure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stealth integration test in short mode")
	}

	logger := slog.Default()
	eng := NewContainerEngine("", logger)

	err := eng.Launch(LaunchOpts{})
	if err != nil {
		t.Fatalf("launch container: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	page, err := eng.NewPage(ctx, "https://nowsecure.nl/")
	if err != nil {
		t.Fatalf("new page: %v", err)
	}
	defer page.Close()

	// nowsecure.nl uses Cloudflare — wait longer for challenge
	waitStable(page, 2*time.Second, 15*time.Second)

	info, _ := page.Info()
	t.Logf("URL: %s", info.URL)
	t.Logf("Title: %s", info.Title)

	// Check if we passed the Cloudflare challenge
	blocked, _ := page.Eval("() => document.title.includes('Just a moment') || document.title.includes('Attention Required') ? 'blocked' : 'passed'")
	val := fmt.Sprintf("%v", blocked.Value)
	t.Logf("Cloudflare: %s", val)

	if val == "blocked" {
		t.Error("Cloudflare challenge not passed")
	}
}

func waitStable(page Page, stableDur, maxWait time.Duration) {
	done := make(chan error, 1)
	go func() { done <- page.WaitStable(stableDur) }()
	select {
	case <-done:
	case <-time.After(maxWait):
	}
}
