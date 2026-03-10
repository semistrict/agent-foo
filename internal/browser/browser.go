package browser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/chromedp/cdproto/network"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const defaultMaxEventBytes int64 = 1 << 30 // 1GB

// Tab represents an open browser tab.
type Tab struct {
	Ctx    context.Context
	Cancel context.CancelFunc
	URL    string
	Title  string
}

// Instance holds a long-lived chromedp browser context.
type Instance struct {
	Ctx         context.Context // active tab's context (used for actions)
	BrowserCtx  context.Context // root browser context (used for creating tabs)
	Events      *EventBuffer
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	Tabs        []*Tab
	ActiveTab   int
}

// findChrome locates the user's installed Chrome binary.
func findChrome() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		candidates := []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	case "linux":
		for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
			if p, err := exec.LookPath(name); err == nil {
				return p, nil
			}
		}
	case "windows":
		candidates := []string{
			os.Getenv("ProgramFiles") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("ProgramFiles(x86)") + `\Google\Chrome\Application\chrome.exe`,
			os.Getenv("LocalAppData") + `\Google\Chrome\Application\chrome.exe`,
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("could not find Chrome installation")
}

// LaunchOptions configures the browser launch.
type LaunchOptions struct {
	Headed  bool
	CDPUrl  string
	Profile string
}

// Launch starts a Chrome instance. The browser stays alive until Close().
func Launch(opts LaunchOptions) (*Instance, error) {
	events := NewEventBuffer(defaultMaxEventBytes)

	if opts.CDPUrl != "" {
		allocCtx, allocCancel := chromedp.NewRemoteAllocator(context.Background(), opts.CDPUrl)
		ctx, cancel := chromedp.NewContext(allocCtx)
		inst := &Instance{Ctx: ctx, BrowserCtx: ctx, Events: events, allocCancel: allocCancel, ctxCancel: cancel}
		// Need to run something to initialize the target before listening
		chromedp.Run(ctx)
		StartListening(ctx, events)
		return inst, nil
	}

	chromePath, err := findChrome()
	if err != nil {
		return nil, err
	}

	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	allocOpts = append(allocOpts, chromedp.ExecPath(chromePath))

	if opts.Headed {
		allocOpts = append(allocOpts, chromedp.Flag("headless", false))
	}

	if opts.Profile != "" {
		allocOpts = append(allocOpts, chromedp.UserDataDir(opts.Profile))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	inst := &Instance{Ctx: ctx, BrowserCtx: ctx, Events: events, allocCancel: allocCancel, ctxCancel: cancel}
	// Initialize browser, enable network + runtime domains, then listen
	chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		network.Enable().Do(ctx)
		cdpruntime.Enable().Do(ctx)
		return nil
	}))
	StartListening(ctx, events)
	return inst, nil
}

// Run executes chromedp actions.
func (b *Instance) Run(actions ...chromedp.Action) error {
	return chromedp.Run(b.Ctx, actions...)
}

// Close shuts down the browser.
func (b *Instance) Close() {
	b.ctxCancel()
	b.allocCancel()
}

// SelectorOrRef interprets a selector string. If it starts with @,
// it's treated as a data-ref attribute selector.
func SelectorOrRef(sel string) string {
	if strings.HasPrefix(sel, "@") {
		return fmt.Sprintf(`[data-ref="%s"]`, sel[1:])
	}
	return sel
}
