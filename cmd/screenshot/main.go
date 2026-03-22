package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/claytono/readest-hardcover-sync/internal/demo"
)

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	server, baseURL, err := demo.StartServer(ctx, logger, ":0", "demo-covers")
	if err != nil {
		return err
	}
	defer server.Close() //nolint:errcheck // best-effort cleanup

	logger.Info("demo server started", "url", baseURL)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	defer allocCancel()

	taskCtx, taskCancel := chromedp.NewContext(allocCtx, chromedp.WithLogf(log.Printf))
	defer taskCancel()

	// Desktop screenshot (1440x900 at 2x for retina quality).
	// CaptureScreenshot captures only the viewport, not the full scrollable page.
	logger.Info("capturing desktop screenshot")
	var desktopBuf []byte
	if err := chromedp.Run(taskCtx,
		chromedp.EmulateViewport(1440, 900, chromedp.EmulateScale(2)),
		chromedp.Navigate(baseURL+"/books"),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.CaptureScreenshot(&desktopBuf),
	); err != nil {
		return err
	}
	if err := os.WriteFile("screenshots/desktop.png", desktopBuf, 0o644); err != nil {
		return err
	}
	logger.Info("captured desktop screenshot", "path", "screenshots/desktop.png")

	// Mobile screenshot (375x812 at 2x for retina quality).
	logger.Info("capturing mobile screenshot")
	var mobileBuf []byte
	if err := chromedp.Run(taskCtx,
		chromedp.EmulateViewport(375, 812, chromedp.EmulateScale(2)),
		chromedp.Navigate(baseURL+"/books"),
		chromedp.WaitReady("body"),
		chromedp.Sleep(2*time.Second),
		chromedp.CaptureScreenshot(&mobileBuf),
	); err != nil {
		return err
	}
	if err := os.WriteFile("screenshots/mobile.png", mobileBuf, 0o644); err != nil {
		return err
	}
	logger.Info("captured mobile screenshot", "path", "screenshots/mobile.png")

	logger.Info("screenshots complete")
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
