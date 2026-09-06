package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/jLemmings/DiveVault-Importer/internal/backend"
	"github.com/jLemmings/DiveVault-Importer/internal/divecomputer"
	"github.com/jLemmings/DiveVault-Importer/internal/importer"
	"github.com/jLemmings/DiveVault-Importer/internal/settings"
)

var appLogger *log.Logger

type desktop struct {
	app                             fyne.App
	window                          fyne.Window
	driver                          divecomputer.Driver
	config                          settings.Settings
	models                          map[string][]string
	token                           string
	preferredPort                   string
	busy                            bool
	cancel                          context.CancelFunc
	vendor, product, port, language *widget.Select
	server                          *widget.Entry
	scan, login, sync, cancelButton *widget.Button
	openLogs                        *widget.Button
	status                          *widget.Label
	progress                        *widget.ProgressBarInfinite
	tr                              func(string) string
	labels                          map[*widget.Label]string
	cards                           map[*widget.Card]string
}

func main() {
	checkRuntime := flag.Bool("check-runtime", false, "verify libdivecomputer and list the number of supported serial models without opening the GUI")
	flag.Parse()
	closeLogger, err := initLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not initialize logging: %v\n", err)
	} else {
		defer closeLogger()
	}
	if *checkRuntime {
		models, err := (divecomputer.Driver{}).Models()
		if err != nil {
			logf("runtime check failed: %v", err)
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		count := 0
		for _, products := range models {
			count += len(products)
		}
		logf("runtime check succeeded: serial_models=%d", count)
		fmt.Printf("DiveSync %s: libdivecomputer ready (%d serial models)\n", version(), count)
		return
	}
	a := app.NewWithID("ch.divevault.importer")
	a.Settings().SetTheme(newDiveVaultTheme())
	iconName := "assets/logo.png"
	if runtime.GOOS == "windows" {
		iconName = "assets/logo.ico"
	}
	icon, _ := resources.ReadFile(iconName)
	appIcon := fyne.NewStaticResource(iconName, icon)
	a.SetIcon(appIcon)
	w := a.NewWindow("DiveSync " + version())
	w.SetIcon(appIcon)
	ui := &desktop{app: a, window: w, config: settings.Load(), labels: map[*widget.Label]string{}, cards: map[*widget.Card]string{}}
	ui.build()
	logf("client started: version=%s backend=%q language=%q", version(), ui.config.BackendURL, ui.config.Language)
	w.Resize(fyne.NewSize(920, 680))
	w.SetCloseIntercept(func() {
		if ui.cancel != nil {
			ui.cancel()
		}
		w.SetCloseIntercept(nil)
		w.Close()
	})
	ui.setBusy(true)
	go func() {
		models, err := ui.driver.Models()
		fyne.Do(func() {
			ui.setBusy(false)
			if err != nil {
				logf("failed to enumerate models: %v", err)
				ui.fail(err)
				return
			}
			ui.models = models
			brands := []string{}
			for brand := range models {
				brands = append(brands, brand)
			}
			sort.Strings(brands)
			ui.vendor.Options = brands
			ui.vendor.Refresh()
			ui.vendor.SetSelected(ui.config.Vendor)
			if ui.vendor.Selected == "" && len(brands) > 0 {
				ui.vendor.SetSelected(brands[0])
			}
			logf("models loaded: brands=%d", len(brands))
		})
	}()
	w.ShowAndRun()
	logf("client stopped")
}

func initLogger() (func(), error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(root, "DiveVault Importer", "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, err
	}
	logPath := filepath.Join(logDir, "client.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	appLogger = log.New(f, "", log.Ldate|log.Ltime|log.Lmicroseconds)
	logf("log initialized: path=%q", logPath)
	return func() {
		logf("closing log file")
		_ = f.Close()
	}, nil
}

func logf(format string, args ...any) {
	if appLogger != nil {
		appLogger.Printf(format, args...)
	}
}
func (d *desktop) label(key string) *widget.Label {
	l := widget.NewLabel(d.tr(key))
	d.labels[l] = key
	return l
}
func (d *desktop) card(key string, content fyne.CanvasObject) *widget.Card {
	c := widget.NewCard(d.tr(key), "", content)
	d.cards[c] = key
	return c
}
func (d *desktop) build() {
	d.preferredPort = d.config.Port
	d.tr = translator(d.config.Language)
	d.status = widget.NewLabel(d.tr("Ready. Sign in to the backend to start syncing."))
	d.status.Wrapping = fyne.TextWrapWord
	d.progress = widget.NewProgressBarInfinite()
	d.progress.Hide()
	d.progress.Stop()
	d.vendor = widget.NewSelect(nil, func(v string) {
		wanted := d.config.Product
		d.config.Vendor = v
		d.port.ClearSelected()
		d.port.Options = nil
		d.port.Refresh()
		d.product.Options = d.models[v]
		d.product.ClearSelected()
		d.product.Refresh()
		for _, p := range d.product.Options {
			if p == wanted {
				d.product.SetSelected(p)
				return
			}
		}
		if len(d.product.Options) > 0 {
			d.product.SetSelected(d.product.Options[0])
		}
		d.persist()
	})
	d.product = widget.NewSelect(nil, func(p string) {
		d.config.Product = p
		d.port.ClearSelected()
		d.port.Options = nil
		d.port.Refresh()
		d.persist()
		d.update()
	})
	d.port = widget.NewSelect(nil, func(p string) {
		if p != "" {
			d.preferredPort = p
		}
		d.config.Port = d.preferredPort
		d.persist()
		d.update()
	})
	d.server = widget.NewEntry()
	d.server.SetText(d.config.BackendURL)
	d.server.OnChanged = func(s string) { d.config.BackendURL = strings.TrimSpace(s); d.token = ""; d.persist(); d.update() }
	d.language = widget.NewSelect([]string{"English", "Deutsch", "Français"}, func(s string) {
		d.config.Language = map[string]string{"English": "en", "Deutsch": "de", "Français": "fr"}[s]
		d.tr = translator(d.config.Language)
		d.persist()
		d.translate()
	})
	d.language.SetSelected(map[string]string{"en": "English", "de": "Deutsch", "fr": "Français"}[d.config.Language])
	d.scan = widget.NewButton(d.tr("SCAN FOR DEVICE"), d.startScan)
	d.login = widget.NewButton(d.tr("SIGN IN"), d.startLogin)
	d.sync = widget.NewButton(d.tr("START SYNCHRONIZATION"), d.startSync)
	d.sync.Importance = widget.HighImportance
	d.cancelButton = widget.NewButton(d.tr("Cancel"), func() {
		if d.cancel != nil {
			d.cancel()
		}
	})
	d.openLogs = widget.NewButton(d.tr("OPEN LOGS"), d.openLogsFolder)
	d.cancelButton.Disable()
	logo, _ := resources.ReadFile("assets/logo_header.png")
	image := canvas.NewImageFromResource(fyne.NewStaticResource("logo_header.png", logo))
	image.FillMode = canvas.ImageFillContain
	image.SetMinSize(fyne.NewSize(260, 70))
	hardware := container.NewVBox(container.NewGridWithColumns(2, container.NewVBox(d.label("BRAND"), d.vendor), container.NewVBox(d.label("MODEL"), d.product)), d.label("PORT SELECTOR"), d.port, d.scan)
	access := container.NewVBox(d.label("VAULT SERVER URL"), d.server, d.login)
	content := container.NewVBox(
		image,
		container.NewGridWithColumns(2, d.card("Connect Hardware", hardware), d.card("Vault Access", access)),
		d.card("Initialize Sync", container.NewVBox(d.sync, d.progress, d.status, d.cancelButton)),
	)
	footerLeft := container.NewHBox(d.label("LANGUAGE"), d.language, d.openLogs)
	footer := container.NewPadded(container.NewBorder(nil, nil, footerLeft, widget.NewLabel("DiveSync "+version()), nil))
	d.window.SetContent(container.NewBorder(nil, footer, nil, nil, container.NewVScroll(container.NewPadded(content))))
	d.update()
}
func (d *desktop) persist() {
	if err := settings.Save(d.config); err != nil {
		d.status.SetText(err.Error())
	}
}
func (d *desktop) translate() {
	for l, k := range d.labels {
		l.SetText(d.tr(k))
	}
	for c, k := range d.cards {
		c.SetTitle(d.tr(k))
	}
	if d.scan != nil {
		d.scan.SetText(d.tr("SCAN FOR DEVICE"))
		d.login.SetText(d.tr("SIGN IN"))
		d.sync.SetText(d.tr("START SYNCHRONIZATION"))
		d.cancelButton.SetText(d.tr("Cancel"))
		d.openLogs.SetText(d.tr("OPEN LOGS"))
	}
}

func (d *desktop) openLogsFolder() {
	root, err := os.UserConfigDir()
	if err != nil {
		d.fail(err)
		return
	}
	base := filepath.Join(root, "DiveVault Importer")
	if err := os.MkdirAll(base, 0700); err != nil {
		d.fail(err)
		return
	}
	logs := filepath.Join(base, "logs")
	target := logs
	if err := os.MkdirAll(target, 0700); err != nil {
		d.fail(err)
		return
	}
	logf("open logs folder requested: target=%q", target)
	if err := openInFileExplorer(target); err != nil {
		d.fail(err)
	}
}

func openInFileExplorer(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

func enabled(w fyne.Disableable, on bool) {
	if on {
		w.Enable()
	} else {
		w.Disable()
	}
}
func (d *desktop) update() {
	if d.scan == nil {
		return
	}
	enabled(d.vendor, !d.busy)
	enabled(d.product, !d.busy)
	enabled(d.port, !d.busy)
	enabled(d.server, !d.busy)
	enabled(d.language, !d.busy)
	enabled(d.scan, !d.busy && d.product.Selected != "")
	enabled(d.login, !d.busy && d.port.Selected != "" && d.config.BackendURL != "")
	enabled(d.sync, !d.busy && d.port.Selected != "" && d.token != "")
	enabled(d.cancelButton, d.busy && d.cancel != nil)
}
func (d *desktop) setBusy(b bool) {
	d.busy = b
	if b {
		d.progress.Show()
		d.progress.Start()
	} else {
		d.progress.Stop()
		d.progress.Hide()
		if d.cancel != nil {
			d.cancel()
			d.cancel = nil
		}
	}
	d.update()
}
func (d *desktop) fail(err error) {
	logf("ui error: %v", err)
	d.status.SetText(err.Error())
	dialog.ShowError(err, d.window)
}
func (d *desktop) startScan() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	d.cancel = cancel
	d.setBusy(true)
	d.status.SetText(d.tr("Scanning..."))
	preferred := d.preferredPort
	v, p := d.vendor.Selected, d.product.Selected
	logf("scan started: vendor=%q product=%q", v, p)
	go func() {
		ports, err := d.driver.Scan(ctx, v, p)
		fyne.Do(func() {
			d.setBusy(false)
			d.port.ClearSelected()
			d.port.Options = ports
			d.port.Refresh()
			if err != nil {
				logf("scan failed: %v", err)
				d.fail(err)
				return
			}
			if len(ports) == 0 {
				logf("scan completed: no device detected")
				d.status.SetText(d.tr("Not detected"))
				return
			}
			d.port.SetSelected(ports[0])
			for _, port := range ports {
				if port == preferred {
					d.port.SetSelected(port)
					break
				}
			}
			logf("scan completed: ports=%v selected=%q", ports, d.port.Selected)
			d.status.SetText(d.tr("Dive computer detected. Sign in to the backend to continue."))
		})
	}()
}
func (d *desktop) startLogin() {
	client, err := backend.New(d.config.BackendURL, "")
	if err != nil {
		d.fail(err)
		return
	}
	logf("login started: backend=%q", d.config.BackendURL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	d.cancel = cancel
	d.setBusy(true)
	d.status.SetText(d.tr("Creating desktop login request..."))
	go func() {
		approval, err := client.StartLogin(ctx)
		if err == nil {
			logf("login request created")
			approvalURL := client.ApprovalURL(approval.Code)
			u, _ := url.Parse(approvalURL)
			fyne.Do(func() {
				d.status.SetText(d.tr("Browser login in progress. Finish approval in the opened browser tab."))
				if openErr := d.app.OpenURL(u); openErr != nil {
					dialog.ShowCustom(d.tr("SIGN IN"), d.tr("Close"), widget.NewHyperlink(approvalURL, u), d.window)
				}
			})
			var token string
			token, err = client.WaitLogin(ctx, approval.Code)
			if err == nil {
				fyne.Do(func() {
					d.token = token
					d.setBusy(false)
					d.status.SetText(d.tr("Backend login completed. You can start the sync."))
					logf("login completed successfully")
				})
				return
			}
		}
		logf("login failed: %v", err)
		fyne.Do(func() { d.setBusy(false); d.fail(err) })
	}()
}
func (d *desktop) startSync() {
	client, err := backend.New(d.config.BackendURL, d.token)
	if err != nil {
		d.fail(err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.setBusy(true)
	port, vendor, product := d.port.Selected, d.vendor.Selected, d.product.Selected
	logf("sync started: vendor=%q product=%q port=%q", vendor, product, port)
	go func() {
		result, err := importer.Sync(ctx, d.driver, client, port, vendor, product, func(r importer.Result) { fyne.Do(func() { d.status.SetText(d.progressText(r)) }) })
		fyne.Do(func() {
			d.setBusy(false)
			if err != nil {
				logf("sync failed: %v", err)
				d.fail(err)
				return
			}
			if result.ExistingTotal != nil {
				logf("sync completed: imported=%d skipped=%d existing_total=%d", result.Imported, result.Skipped, *result.ExistingTotal)
			} else {
				logf("sync completed: imported=%d skipped=%d", result.Imported, result.Skipped)
			}
			d.status.SetText(d.resultText(result))
		})
	}()
}
func (d *desktop) progressText(r importer.Result) string {
	return strings.NewReplacer("{imported}", fmt.Sprint(r.Imported), "{skipped}", fmt.Sprint(r.Skipped)).Replace(d.tr("Sync in progress. {imported} dives synced, {skipped} already present."))
}
func (d *desktop) resultText(r importer.Result) string {
	if r.Imported == 0 && r.ExistingTotal != nil {
		return strings.ReplaceAll(d.tr("No new dives to sync. {total} dives already present in the backend."), "{total}", fmt.Sprint(*r.ExistingTotal))
	}
	return strings.NewReplacer("{imported}", fmt.Sprint(r.Imported), "{skipped}", fmt.Sprint(r.Skipped)).Replace(d.tr("Sync completed successfully. {imported} dives synced, {skipped} already present."))
}
