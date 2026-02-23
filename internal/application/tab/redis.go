package tab

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	"github.com/i4erkasov/proto-viewer/internal/application/widgets/colorbutton"
	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/repository"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/secret"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

const (
	prefRedisHost         = "redis.host"
	prefRedisPort         = "redis.port"
	prefRedisTLS          = "redis.tls"
	prefRedisUsername     = "redis.username"
	prefRedisSavePassword = "redis.savePassword"
	prefRedisPassEnc      = "redis.passwordEnc"
	prefRedisGzip         = "redis.gzip"
)

type RedisTab struct {
	w fyne.Window

	repo domain.RedisRepository

	host *widget.Entry
	port *widget.Entry

	tlsCheck *widget.Check

	user *widget.Entry
	pass *widget.Entry

	savePass *widget.Check

	dbSelect    *widget.Select
	keySelect   *widget.Select
	fieldSelect *widget.Select
	fieldLabel  *widget.Label

	// Wrapper shown/hidden together with Field label+select
	fieldWrap *fyne.Container

	selectedDB    int
	selectedKey   string
	selectedField string
	keyType       domain.RedisKeyType // "" | "string" | "hash"

	tbtnTest *widget.Button
	btnWrap  *colorbutton.Button

	connected bool

	root fyne.CanvasObject

	gzipCheck *widget.Check
}

func NewTabRedis(w fyne.Window) *RedisTab {
	t := &RedisTab{w: w, repo: repository.NewRedis()}

	prefs := fyne.CurrentApp().Preferences()

	t.host = widget.NewEntry()
	t.host.SetText("127.0.0.1")
	if v := strings.TrimSpace(prefs.String(prefRedisHost)); v != "" {
		t.host.SetText(v)
	}

	t.port = widget.NewEntry()
	t.port.SetText("6379")
	if v := strings.TrimSpace(prefs.String(prefRedisPort)); v != "" {
		t.port.SetText(v)
	}

	t.tlsCheck = widget.NewCheck("TLS", func(checked bool) {
		prefs.SetBool(prefRedisTLS, checked)
	})
	t.tlsCheck.SetChecked(prefs.Bool(prefRedisTLS))

	t.user = widget.NewEntry()
	t.user.SetPlaceHolder("(optional)")
	if v := strings.TrimSpace(prefs.String(prefRedisUsername)); v != "" {
		t.user.SetText(v)
	}

	t.pass = widget.NewPasswordEntry()
	t.pass.SetPlaceHolder("(optional)")

	// Restore saved password if enabled
	t.savePass = widget.NewCheck("Save password", func(checked bool) {
		prefs.SetBool(prefRedisSavePassword, checked)
		if !checked {
			// If user disabled saving - wipe stored secret.
			prefs.SetString(prefRedisPassEnc, "")
		}
	})
	if prefs.Bool(prefRedisSavePassword) {
		t.savePass.SetChecked(true)
		enc := strings.TrimSpace(prefs.String(prefRedisPassEnc))
		if enc != "" {
			key := secret.DeriveKey(fyne.CurrentApp().UniqueID() + "|proto-viewer|redis")
			if p, err := secret.DecryptString(key, enc); err == nil {
				t.pass.SetText(p)
			}
		}
	}

	t.dbSelect = widget.NewSelect([]string{}, nil)
	t.dbSelect.PlaceHolder = "Select DB…"
	t.dbSelect.Disable()

	t.keySelect = widget.NewSelect([]string{}, nil)
	t.keySelect.PlaceHolder = "Select key…"
	t.keySelect.Disable()

	t.fieldSelect = widget.NewSelect([]string{}, nil)
	t.fieldSelect.PlaceHolder = "Select hash field…"
	t.fieldSelect.Hide()

	t.fieldLabel = widget.NewLabel("Field:")
	t.fieldLabel.Hide()

	// Custom dark-red connect/disconnect button.
	// Keep default label styling (white on dark theme) by still using widget.Button.
	const connectBtnW float32 = 140
	connectBlue := color.NRGBA{R: 0x2d, G: 0x8c, B: 0xff, A: 0xff}
	cb := colorbutton.New("Connect", connectBlue, func() {
		t.onConnectButton()
	})
	// Start in disconnected state (blue). When connected it becomes dark red (#7c0a02).
	t.btnWrap = cb

	// Persist host/port/user/tls when user edits (cheap + immediate)
	t.host.OnChanged = func(s string) { prefs.SetString(prefRedisHost, strings.TrimSpace(s)) }
	t.port.OnChanged = func(s string) { prefs.SetString(prefRedisPort, strings.TrimSpace(s)) }
	t.user.OnChanged = func(s string) { prefs.SetString(prefRedisUsername, strings.TrimSpace(s)) }

	// GZIP checkbox placed after Key selector.
	t.gzipCheck = widget.NewCheck("GZIP compressed", func(checked bool) {
		prefs.SetBool(prefRedisGzip, checked)
	})
	t.gzipCheck.SetChecked(prefs.Bool(prefRedisGzip))
	gzipWrap := container.NewGridWrap(t.gzipCheck.MinSize(), t.gzipCheck)

	// --- Row 1: Host / Port / TLS
	entryH := t.host.MinSize().Height
	hostWrap := container.NewGridWrap(fyne.NewSize(260, entryH), t.host)
	portWrap := container.NewGridWrap(fyne.NewSize(110, entryH), t.port)
	tlsWrap := container.NewGridWrap(t.tlsCheck.MinSize(), t.tlsCheck)

	connRow1 := container.NewHBox(
		widget.NewLabel("Host:"), hostWrap,
		widget.NewLabel("Port:"), portWrap,
		tlsWrap,
	)

	// --- Row 2: Username / Password / Save password / Connect
	userWrap := container.NewGridWrap(fyne.NewSize(200, entryH), t.user)
	passWrap := container.NewGridWrap(fyne.NewSize(230, entryH), t.pass)
	saveWrap := container.NewGridWrap(t.savePass.MinSize(), t.savePass)

	// Make button a bit wider and a bit taller than default.
	btnMin := cb.MinSize()
	testWrap := container.NewGridWrap(fyne.NewSize(connectBtnW, btnMin.Height), cb)

	connRow2 := container.NewHBox(
		widget.NewLabel("User:"), userWrap,
		widget.NewLabel("Password:"), passWrap,
		saveWrap,
		testWrap,
		layout.NewSpacer(),
	)

	// DB + Key + Field in one row
	dbWrap := container.NewGridWrap(fyne.NewSize(140, t.dbSelect.MinSize().Height), t.dbSelect)
	keyWrap := container.NewGridWrap(fyne.NewSize(360, t.keySelect.MinSize().Height), t.keySelect)

	fieldWrap := container.NewGridWrap(fyne.NewSize(320, t.fieldSelect.MinSize().Height), t.fieldSelect)
	fieldWrap.Hide()
	t.fieldWrap = fieldWrap

	selectorsRow := container.NewHBox(
		widget.NewLabel("DB:"), dbWrap,
		widget.NewLabel("Key:"), keyWrap,
		gzipWrap,
		t.fieldLabel, fieldWrap,
	)

	t.dbSelect.OnChanged = func(s string) {
		t.onDBSelected(s)
	}
	t.keySelect.OnChanged = func(s string) {
		t.onKeySelected(s)
	}
	t.fieldSelect.OnChanged = func(s string) {
		t.selectedField = s
	}

	t.root = container.NewVBox(
		widget.NewLabel("Connection"),
		container.NewPadded(connRow1),
		layout.NewSpacer(),
		container.NewPadded(connRow2),
		layout.NewSpacer(),
		container.NewPadded(selectorsRow),
	)

	return t
}

func (t *RedisTab) Title() string           { return "Redis" }
func (t *RedisTab) View() fyne.CanvasObject { return t.root }

func (t *RedisTab) cfg() (domain.RedisConfig, error) {
	host := strings.TrimSpace(t.host.Text)
	portStr := strings.TrimSpace(t.port.Text)
	if host == "" || portStr == "" {
		return domain.RedisConfig{}, fmt.Errorf("fill host/port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return domain.RedisConfig{}, fmt.Errorf("port must be a number")
	}

	return domain.RedisConfig{
		Host:     host,
		Port:     port,
		TLS:      t.tlsCheck.Checked,
		Username: strings.TrimSpace(t.user.Text),
		Password: t.pass.Text,
	}, nil
}

func (t *RedisTab) resetAfterConnect() {
	// Only reset selectors/state related to browsing keys.
	// IMPORTANT: don't touch `t.connected` here, because this function is also
	// used by connect flow. Connection state is handled by onConnectButton/
	// testAndLoad and updateConnectButton.
	//
	// This bug caused the Connect/Disconnect button to stay blue because
	// `connected` was forced to false right after switching it to true.

	t.dbSelect.Options = nil
	t.dbSelect.SetSelected("")
	t.dbSelect.Refresh()
	t.dbSelect.Disable()

	t.keySelect.Options = nil
	t.keySelect.SetSelected("")
	t.keySelect.Refresh()
	t.keySelect.Disable()

	t.fieldSelect.Options = nil
	t.fieldSelect.SetSelected("")
	t.fieldSelect.Refresh()
	t.fieldSelect.Hide()

	fyne.Do(func() {
		if t.fieldLabel != nil {
			t.fieldLabel.Hide()
			t.fieldLabel.Refresh()
		}
		if t.fieldWrap != nil {
			t.fieldWrap.Hide()
			t.fieldWrap.Refresh()
		}
	})

	t.selectedDB = 0
	t.selectedKey = ""
	t.selectedField = ""
	t.keyType = ""

	// DO NOT set t.connected here
	// DO NOT call t.updateConnectButton() here
}

func (t *RedisTab) onConnectButton() {
	// Prevent double actions while an async connect is in-flight.
	if t.btnWrap != nil && t.btnWrap.Disabled() {
		return
	}

	if t.connected {
		// Logical disconnect (we don't hold a persistent connection).
		t.connected = false
		fyne.Do(func() {
			t.updateConnectButton()
			t.resetAfterConnect()
		})
		return
	}
	t.testAndLoad()
}

func (t *RedisTab) updateConnectButton() {
	if t.btnWrap == nil {
		return
	}

	// Keep colors stable and predictable.
	connectBlue := color.NRGBA{R: 0x2d, G: 0x8c, B: 0xff, A: 0xff}
	disconnectRed := color.NRGBA{R: 0x7c, G: 0x0a, B: 0x02, A: 0xff} // #7c0a02

	if t.connected {
		t.btnWrap.SetText("Disconnect")
		t.btnWrap.SetBackground(disconnectRed)
	} else {
		t.btnWrap.SetText("Connect")
		t.btnWrap.SetBackground(connectBlue)
	}

	t.btnWrap.Refresh()
}

func (t *RedisTab) testAndLoad() {
	// Reset pickers, but keep connection state untouched.
	fyne.Do(func() {
		t.resetAfterConnect()
		if t.btnWrap != nil {
			t.btnWrap.SetDisabled(true)
			t.btnWrap.SetText("Connecting...")
		}
	})
	go func() {
		cfg, err := t.cfg()
		if err != nil {
			fyne.Do(func() {
				if t.btnWrap != nil {
					t.btnWrap.SetDisabled(false)
					t.updateConnectButton()
				}
			})
			dialog.ShowError(err, t.w)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		if err := t.repo.Ping(ctx, cfg); err != nil {
			fyne.Do(func() {
				if t.btnWrap != nil {
					t.btnWrap.SetDisabled(false)
					t.updateConnectButton()
				}
			})
			dialog.ShowError(err, t.w)
			return
		}

		// Save preferences after successful connection test
		prefs := fyne.CurrentApp().Preferences()
		prefs.SetString(prefRedisHost, strings.TrimSpace(t.host.Text))
		prefs.SetString(prefRedisPort, strings.TrimSpace(t.port.Text))
		prefs.SetBool(prefRedisTLS, t.tlsCheck.Checked)
		prefs.SetString(prefRedisUsername, strings.TrimSpace(t.user.Text))
		prefs.SetBool(prefRedisSavePassword, t.savePass.Checked)
		if t.savePass.Checked {
			key := secret.DeriveKey(fyne.CurrentApp().UniqueID() + "|proto-viewer|redis")
			enc, err := secret.EncryptString(key, t.pass.Text)
			if err == nil {
				prefs.SetString(prefRedisPassEnc, enc)
			}
		} else {
			prefs.SetString(prefRedisPassEnc, "")
		}

		dbs, err := t.repo.DBsWithKeys(ctx, cfg, 0, 31)
		if err != nil {
			fyne.Do(func() {
				if t.btnWrap != nil {
					t.btnWrap.SetDisabled(false)
					t.updateConnectButton()
				}
			})
			dialog.ShowError(err, t.w)
			return
		}

		opts := make([]string, 0, len(dbs))
		for _, db := range dbs {
			opts = append(opts, strconv.Itoa(db))
		}

		fyne.Do(func() {
			t.dbSelect.Options = opts
			t.dbSelect.SetSelected("")
			t.dbSelect.Refresh()
			if len(opts) > 0 {
				t.dbSelect.Enable()
			}
			t.connected = true
			if t.btnWrap != nil {
				t.btnWrap.SetDisabled(false)
			}
			t.updateConnectButton()
		})
	}()
}

func (t *RedisTab) onDBSelected(s string) {
	t.keySelect.Options = nil
	t.keySelect.SetSelected("")
	t.keySelect.Refresh()
	t.keySelect.Disable()

	t.fieldSelect.Options = nil
	t.fieldSelect.SetSelected("")
	t.fieldSelect.Refresh()
	t.fieldSelect.Hide()

	fyne.Do(func() {
		if t.fieldLabel != nil {
			t.fieldLabel.Hide()
			t.fieldLabel.Refresh()
		}
		if t.fieldWrap != nil {
			t.fieldWrap.Hide()
			t.fieldWrap.Refresh()
		}
	})

	t.selectedKey = ""
	t.selectedField = ""
	t.keyType = ""

	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	dbInt, err := strconv.Atoi(s)
	if err != nil {
		dialog.ShowError(fmt.Errorf("db must be a number"), t.w)
		return
	}
	t.selectedDB = dbInt

	go func() {
		cfg, err := t.cfg()
		if err != nil {
			dialog.ShowError(err, t.w)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		keys, _ := t.repo.Keys(ctx, cfg, dbInt, "*", 2000, 200)

		fyne.Do(func() {
			t.keySelect.Options = keys
			t.keySelect.SetSelected("")
			t.keySelect.Refresh()
			if len(keys) > 0 {
				t.keySelect.Enable()
			}
		})
	}()
}

func (t *RedisTab) onKeySelected(k string) {
	t.selectedKey = strings.TrimSpace(k)
	t.selectedField = ""
	t.keyType = ""

	t.fieldSelect.Options = nil
	t.fieldSelect.SetSelected("")
	t.fieldSelect.Refresh()
	t.fieldSelect.Hide()

	fyne.Do(func() {
		if t.fieldWrap != nil {
			t.fieldWrap.Hide()
			t.fieldWrap.Refresh()
		}
		if t.fieldLabel != nil {
			t.fieldLabel.Hide()
			t.fieldLabel.Refresh()
		}
	})

	if t.selectedKey == "" {
		return
	}

	go func() {
		cfg, err := t.cfg()
		if err != nil {
			dialog.ShowError(err, t.w)
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tp, err := t.repo.KeyType(ctx, cfg, t.selectedDB, t.selectedKey)
		if err != nil {
			dialog.ShowError(err, t.w)
			return
		}
		t.keyType = tp

		switch tp {
		case domain.RedisKeyTypeString:
			fyne.Do(func() {
				t.fieldSelect.Hide()
				t.fieldSelect.Refresh()
				if t.fieldWrap != nil {
					t.fieldWrap.Hide()
					t.fieldWrap.Refresh()
				}
				if t.fieldLabel != nil {
					t.fieldLabel.Hide()
					t.fieldLabel.Refresh()
				}
			})
		case domain.RedisKeyTypeHash:
			fields, err := t.repo.HashFields(ctx, cfg, t.selectedDB, t.selectedKey)
			if err != nil {
				dialog.ShowError(err, t.w)
				return
			}
			fyne.Do(func() {
				t.fieldSelect.Options = fields
				t.fieldSelect.SetSelected("")
				t.fieldSelect.Refresh()
				t.fieldSelect.Show()
				if t.fieldWrap != nil {
					t.fieldWrap.Show()
					t.fieldWrap.Refresh()
				}
				if t.fieldLabel != nil {
					t.fieldLabel.Show()
					t.fieldLabel.Refresh()
				}
			})
		default:
			fyne.Do(func() {
				t.fieldSelect.Hide()
				t.fieldSelect.Refresh()
				if t.fieldWrap != nil {
					t.fieldWrap.Hide()
					t.fieldWrap.Refresh()
				}
				if t.fieldLabel != nil {
					t.fieldLabel.Hide()
					t.fieldLabel.Refresh()
				}
			})
		}
	}()
}

// Gzip returns current GZIP checkbox state.
func (t *RedisTab) Gzip() bool {
	if t.gzipCheck == nil {
		return false
	}
	return t.gzipCheck.Checked
}

func (t *RedisTab) Fetch(ctx context.Context) ([]byte, error) {
	cfg, err := t.cfg()
	if err != nil {
		return nil, err
	}
	if t.selectedKey == "" {
		return nil, fmt.Errorf("select redis key")
	}

	switch t.keyType {
	case "", domain.RedisKeyTypeString:
		return t.repo.Get(ctx, cfg, t.selectedDB, t.selectedKey)
	case domain.RedisKeyTypeHash:
		if strings.TrimSpace(t.selectedField) == "" {
			return nil, fmt.Errorf("select hash field")
		}
		return t.repo.HGet(ctx, cfg, t.selectedDB, t.selectedKey, t.selectedField)
	default:
		return nil, fmt.Errorf("unsupported redis key type: %s", t.keyType)
	}
}

// SelectedKey returns currently selected Redis key (empty if none).
func (t *RedisTab) SelectedKey() string {
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.selectedKey)
}
