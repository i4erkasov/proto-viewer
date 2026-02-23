package tab

import (
	"context"
	"fmt"
	"image/color"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/i4erkasov/proto-viewer/internal/application/components/colorbutton"
	"github.com/i4erkasov/proto-viewer/internal/application/components/searchselect"
	sqlrepo "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sql"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqldata"
	sqlmeta "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqlmeta"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/secret"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type sqlDriver string

const (
	sqlDriverPostgres sqlDriver = "Postgres"
	sqlDriverMySQL    sqlDriver = "MySQL"
)

const (
	prefSQLDriver       = "sql.driver"
	prefSQLHost         = "sql.host"
	prefSQLPort         = "sql.port"
	prefSQLTLS          = "sql.tls"
	prefSQLUsername     = "sql.username"
	prefSQLDB           = "sql.db"
	prefSQLSchema       = "sql.schema"
	prefSQLSavePassword = "sql.savePassword"
	prefSQLPassEnc      = "sql.passwordEnc"
)

type SQLTab struct {
	w fyne.Window

	driver *widget.Select

	host *widget.Entry
	port *widget.Entry
	tls  *widget.Check

	user *widget.Entry
	pass *widget.Entry

	savePass *widget.Check

	btnWrap   *colorbutton.Button
	connected bool

	db *widget.Entry

	tschema *searchselect.SearchableSelect
	ttable  *searchselect.SearchableSelect
	tcolumn *searchselect.SearchableSelect

	// Full lists from DB
	allSchemas []string
	allTables  []string
	allColumns []string

	// Where clause controls (multiple conditions)
	whereRows      []*sqlWhereRow
	whereContainer *fyne.Container
	btnAddWhere    *widget.Button
	whereJoin      *widget.Select // AND / OR

	repo sqlmeta.Repo

	root fyne.CanvasObject
}

type sqlWhereRow struct {
	fieldSel *searchselect.SearchableSelect
	op       *widget.Select
	value    *widget.Entry
	row      *fyne.Container
}

func NewTabSQL(w fyne.Window) *SQLTab {
	t := &SQLTab{w: w}

	prefs := fyne.CurrentApp().Preferences()

	// Driver select
	t.driver = widget.NewSelect([]string{string(sqlDriverPostgres), string(sqlDriverMySQL)}, func(string) {})
	t.driver.PlaceHolder = "Driver"
	if v := strings.TrimSpace(prefs.String(prefSQLDriver)); v != "" {
		t.driver.SetSelected(v)
	} else {
		t.driver.SetSelected(string(sqlDriverPostgres))
	}

	// Persist driver + adjust DB default if DB still looks like default/empty.
	t.driver.OnChanged = func(s string) {
		s = strings.TrimSpace(s)
		prefs.SetString(prefSQLDriver, s)

		// If user hasn't set anything meaningful yet, keep DB in sync with driver default.
		curDB := strings.TrimSpace(t.db.Text)
		if curDB == "" || curDB == "postgres" || curDB == "mysql" {
			switch sqlDriver(s) {
			case sqlDriverMySQL:
				t.db.SetText("mysql")
			case sqlDriverPostgres:
				fallthrough
			default:
				t.db.SetText("postgres")
			}
		}
	}

	t.host = widget.NewEntry()
	t.host.SetText("127.0.0.1")
	if v := strings.TrimSpace(prefs.String(prefSQLHost)); v != "" {
		t.host.SetText(v)
	}

	t.port = widget.NewEntry()
	t.port.SetText("5432")
	if v := strings.TrimSpace(prefs.String(prefSQLPort)); v != "" {
		t.port.SetText(v)
	}

	t.tls = widget.NewCheck("TLS", func(checked bool) {
		prefs.SetBool(prefSQLTLS, checked)
	})
	t.tls.SetChecked(prefs.Bool(prefSQLTLS))

	t.user = widget.NewEntry()
	t.user.SetPlaceHolder("(optional)")
	if v := strings.TrimSpace(prefs.String(prefSQLUsername)); v != "" {
		t.user.SetText(v)
	}

	t.pass = widget.NewPasswordEntry()
	t.pass.SetPlaceHolder("(optional)")

	t.savePass = widget.NewCheck("Save password", func(checked bool) {
		prefs.SetBool(prefSQLSavePassword, checked)
		if !checked {
			prefs.SetString(prefSQLPassEnc, "")
		}
	})
	if prefs.Bool(prefSQLSavePassword) {
		t.savePass.SetChecked(true)
		enc := strings.TrimSpace(prefs.String(prefSQLPassEnc))
		if enc != "" {
			key := secret.DeriveKey(fyne.CurrentApp().UniqueID() + "|proto-viewer|sql")
			if p, err := secret.DecryptString(key, enc); err == nil {
				t.pass.SetText(p)
			}
		}
	}

	// Persist host/port/user/db/schema when user edits (cheap + immediate)
	t.host.OnChanged = func(s string) { prefs.SetString(prefSQLHost, strings.TrimSpace(s)) }
	t.port.OnChanged = func(s string) { prefs.SetString(prefSQLPort, strings.TrimSpace(s)) }
	t.user.OnChanged = func(s string) { prefs.SetString(prefSQLUsername, strings.TrimSpace(s)) }

	// Reuse the same button style as Redis.
	connectBlue := color.NRGBA{R: 0x2d, G: 0x8c, B: 0xff, A: 0xff}
	cb := colorbutton.New("Connect", connectBlue, func() {
		t.onConnectButton()
	})
	t.btnWrap = cb

	t.db = widget.NewEntry()
	t.db.SetPlaceHolder("database")
	if v := strings.TrimSpace(prefs.String(prefSQLDB)); v != "" {
		t.db.SetText(v)
	} else {
		// Driver-specific defaults (only when there's no saved value).
		switch sqlDriver(t.driver.Selected) {
		case sqlDriverMySQL:
			t.db.SetText("mysql")
		case sqlDriverPostgres:
			fallthrough
		default:
			t.db.SetText("postgres")
		}
	}
	t.db.OnChanged = func(s string) { prefs.SetString(prefSQLDB, strings.TrimSpace(s)) }

	// Schema/Table/Column searchable selects (filled after connect)
	t.tschema = searchselect.NewSearchableSelect(w, "Select...", nil)
	t.tschema.OnChanged = func(s string) {
		s = strings.TrimSpace(s)
		prefs.SetString(prefSQLSchema, s)
		if !t.connected {
			return
		}
		t.loadTablesAndColumnsAsync(s, "")
	}
	t.tschema.Disable()

	t.ttable = searchselect.NewSearchableSelect(w, "Select...", nil)
	t.ttable.OnChanged = func(s string) {
		s = strings.TrimSpace(s)
		if !t.connected {
			return
		}
		t.loadColumnsAsync(strings.TrimSpace(t.tschema.Selected()), s, "")
	}
	t.ttable.Disable()

	t.tcolumn = searchselect.NewSearchableSelect(w, "Select...", nil)
	t.tcolumn.OnChanged = func(_ string) {}
	t.tcolumn.Disable()

	// --- WHERE (multiple conditions)
	// AND/OR join select
	t.whereJoin = widget.NewSelect([]string{"AND", "OR"}, func(string) {})
	t.whereJoin.SetSelected("AND")

	newWhereRow := func() *sqlWhereRow {
		r := &sqlWhereRow{}
		r.fieldSel = searchselect.NewSearchableSelect(w, "field", nil)
		r.op = widget.NewSelect([]string{"=", "!=", ">", ">=", "<", "<=", "like"}, func(string) {})
		r.op.SetSelected("=")
		r.value = widget.NewEntry()
		r.value.SetPlaceHolder("value")
		return r
	}

	entryH := float32(36)
	wfWrap := func(s *searchselect.SearchableSelect) fyne.CanvasObject {
		return container.NewGridWrap(fyne.NewSize(220, entryH), s)
	}
	woWrap := func(s *widget.Select) fyne.CanvasObject {
		return container.NewGridWrap(fyne.NewSize(90, entryH), s)
	}
	wvWrap := func(e *widget.Entry) fyne.CanvasObject {
		return container.NewGridWrap(fyne.NewSize(260, entryH), e)
	}

	addWhereRowUI := func(r *sqlWhereRow) {
		removeBtn := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
			// remove this row
			for i := range t.whereRows {
				if t.whereRows[i] == r {
					t.whereRows = append(t.whereRows[:i], t.whereRows[i+1:]...)
					break
				}
			}
			if t.whereContainer != nil {
				t.whereContainer.Remove(r.row)
				t.whereContainer.Refresh()
			}
		})
		removeWrap := container.NewGridWrap(fyne.NewSize(40, entryH), removeBtn)

		r.row = container.NewHBox(
			wfWrap(r.fieldSel),
			woWrap(r.op),
			wvWrap(r.value),
			removeWrap,
			layout.NewSpacer(),
		)
		if t.whereContainer != nil {
			t.whereContainer.Add(r.row)
		}
	}

	// WHERE container + add button
	t.whereContainer = container.NewVBox()
	t.btnAddWhere = widget.NewButtonWithIcon("Add condition", theme.ContentAddIcon(), func() {
		r := newWhereRow()
		t.whereRows = append(t.whereRows, r)
		addWhereRowUI(r)
		t.whereContainer.Refresh()
	})

	// start with a single empty condition row
	{
		r := newWhereRow()
		t.whereRows = append(t.whereRows, r)
		addWhereRowUI(r)
	}

	// --- layout
	entryH = float32(36)

	driverWrap := container.NewGridWrap(fyne.NewSize(140, entryH), t.driver)
	hostWrap := container.NewGridWrap(fyne.NewSize(220, entryH), t.host)
	// Port should fit up to 6-7 digits (65535, 5432, etc)
	portWrap := container.NewGridWrap(fyne.NewSize(80, entryH), t.port)
	dbWrap := container.NewGridWrap(fyne.NewSize(180, entryH), t.db)
	// Don't call MinSize() on a disabled Select (it can crash in fyne 2.7.x if popUp is nil)
	tlsWrap := container.NewGridWrap(fyne.NewSize(70, entryH), t.tls)

	connRow1 := container.NewHBox(
		widget.NewLabel("Driver:"), driverWrap,
		widget.NewLabel("Host:"), hostWrap,
		widget.NewLabel("Port:"), portWrap,
		widget.NewLabel("DB:"), dbWrap,
		tlsWrap,
		layout.NewSpacer(),
	)

	// Row 2: user/pass/save password/connect
	userWrap := container.NewGridWrap(fyne.NewSize(200, entryH), t.user)
	passWrap := container.NewGridWrap(fyne.NewSize(230, entryH), t.pass)
	// same: avoid MinSize() on checks/selects for safety
	saveWrap := container.NewGridWrap(fyne.NewSize(160, entryH), t.savePass)
	btnMin := cb.MinSize()
	btnWrap := container.NewGridWrap(fyne.NewSize(150, btnMin.Height), cb)
	connRow2 := container.NewHBox(
		widget.NewLabel("User:"), userWrap,
		widget.NewLabel("Password:"), passWrap,
		saveWrap,
		btnWrap,
		layout.NewSpacer(),
	)

	// Row 3: Schema / Table / Column (searchable selects)
	schemaWrap := container.NewGridWrap(fyne.NewSize(240, entryH), t.tschema)
	tableWrap := container.NewGridWrap(fyne.NewSize(320, entryH), t.ttable)
	colWrap := container.NewGridWrap(fyne.NewSize(320, entryH), t.tcolumn)

	selectorsRow := container.NewHBox(
		widget.NewLabel("Schema:"), schemaWrap,
		widget.NewLabel("Table:"), tableWrap,
		widget.NewLabel("Column:"), colWrap,
		layout.NewSpacer(),
	)

	whereHeaderRow := container.NewHBox(
		widget.NewLabel("Where:"),
		container.NewGridWrap(fyne.NewSize(80, entryH), t.whereJoin),
		container.NewGridWrap(fyne.NewSize(160, entryH), t.btnAddWhere),
		layout.NewSpacer(),
	)

	filtersBody := container.NewVBox(
		container.NewPadded(selectorsRow),
		gap(8),
		container.NewPadded(whereHeaderRow),
		gap(6),
		container.NewPadded(t.whereContainer),
	)
	filtersBody.Hide()

	// Collapsible header
	arrow := widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), nil)
	arrow.Importance = widget.LowImportance
	headerLabel := widget.NewLabel("Filters")
	headerBtn := widget.NewButton("", func() {
		if filtersBody.Visible() {
			filtersBody.Hide()
			arrow.SetIcon(theme.MenuDropDownIcon())
		} else {
			filtersBody.Show()
			arrow.SetIcon(theme.MenuDropUpIcon())
		}
		filtersBody.Refresh()
	})
	headerBtn.Importance = widget.LowImportance
	header := container.NewBorder(nil, nil, headerLabel, arrow, headerBtn)

	// Click on arrow should also toggle
	arrow.OnTapped = func() { headerBtn.OnTapped() }

	// Root
	t.root = container.NewVBox(
		widget.NewLabel("SQL"),
		container.NewPadded(connRow1),
		gap(6),
		container.NewPadded(connRow2),
		gap(10),
		container.NewPadded(header),
		gap(4),
		filtersBody,
	)

	return t
}

// gap is a tiny vertical spacer helper (like a CSS margin) used to keep row spacing consistent.
func gap(h float32) fyne.CanvasObject {
	return container.NewGridWrap(fyne.NewSize(1, h), widget.NewLabel(""))
}

func (t *SQLTab) Title() string           { return "SQL" }
func (t *SQLTab) View() fyne.CanvasObject { return t.root }

func (t *SQLTab) validateCfg() error {
	host := strings.TrimSpace(t.host.Text)
	portStr := strings.TrimSpace(t.port.Text)
	if host == "" || portStr == "" {
		return fmt.Errorf("host and port are required")
	}
	if _, err := strconv.Atoi(portStr); err != nil {
		return fmt.Errorf("port must be a number")
	}
	// DB is optional at this stage: some setups rely on driver defaults or connect to a default database.
	// We'll still persist it if provided.
	return nil
}

func (t *SQLTab) onConnectButton() {
	if t.btnWrap != nil && t.btnWrap.Disabled() {
		return
	}

	if t.connected {
		t.connected = false
		fyne.Do(func() {
			t.updateConnectButton()
			t.resetAfterDisconnect()
		})
		return
	}

	fyne.Do(func() {
		if t.btnWrap != nil {
			t.btnWrap.SetDisabled(true)
			t.btnWrap.SetText("Connecting...")
		}
	})

	go func() {
		if err := t.validateCfg(); err != nil {
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
		prefs.SetString(prefSQLDriver, strings.TrimSpace(t.driver.Selected))
		prefs.SetString(prefSQLHost, strings.TrimSpace(t.host.Text))
		prefs.SetString(prefSQLPort, strings.TrimSpace(t.port.Text))
		prefs.SetBool(prefSQLTLS, t.tls.Checked)
		prefs.SetString(prefSQLUsername, strings.TrimSpace(t.user.Text))
		prefs.SetString(prefSQLDB, strings.TrimSpace(t.db.Text))
		prefs.SetBool(prefSQLSavePassword, t.savePass.Checked)
		if t.savePass.Checked {
			key := secret.DeriveKey(fyne.CurrentApp().UniqueID() + "|proto-viewer|sql")
			if enc, err := secret.EncryptString(key, t.pass.Text); err == nil {
				prefs.SetString(prefSQLPassEnc, enc)
			}
		} else {
			prefs.SetString(prefSQLPassEnc, "")
		}

		driverID, cfg, err := t.sqlCfg()
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

		repo, err := sqlrepo.New(driverID)
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
		t.repo = repo

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

		fyne.Do(func() {
			t.connected = true
			if t.btnWrap != nil {
				t.btnWrap.SetDisabled(false)
			}
			t.updateConnectButton()
			// enable schema; options loaded async
			t.tschema.Enable()
			t.ttable.Disable()
			t.tcolumn.Disable()
		})

		t.loadSchemasAsync()
	}()
}

func (t *SQLTab) resetAfterDisconnect() {
	if t.ttable != nil {
		t.ttable.SetOptions(nil)
		t.ttable.SetSelected("")
		t.ttable.Disable()
	}

	if t.tcolumn != nil {
		t.tcolumn.SetOptions(nil)
		t.tcolumn.SetSelected("")
		t.tcolumn.Disable()
	}

	if t.tschema != nil {
		t.tschema.SetOptions(nil)
		t.tschema.SetSelected("")
		t.tschema.Disable()
	}

	// full lists
	t.allSchemas = nil
	t.allTables = nil
	t.allColumns = nil
}

func (t *SQLTab) sqlCfg() (string, sqlmeta.Config, error) {
	port, err := strconv.Atoi(strings.TrimSpace(t.port.Text))
	if err != nil {
		return "", sqlmeta.Config{}, fmt.Errorf("port must be a number")
	}

	driverID := strings.ToLower(strings.TrimSpace(t.driver.Selected))
	switch sqlDriver(t.driver.Selected) {
	case sqlDriverPostgres:
		driverID = sqlrepo.DriverPostgres
	case sqlDriverMySQL:
		driverID = sqlrepo.DriverMySQL
	}

	return driverID, sqlmeta.Config{
		Host:     strings.TrimSpace(t.host.Text),
		Port:     port,
		TLS:      t.tls.Checked,
		User:     strings.TrimSpace(t.user.Text),
		Password: t.pass.Text,
		DB:       strings.TrimSpace(t.db.Text),
	}, nil
}

func (t *SQLTab) defaultSchemaForDriver() string {
	switch sqlDriver(t.driver.Selected) {
	case sqlDriverMySQL:
		return "mysql"
	case sqlDriverPostgres:
		fallthrough
	default:
		return "public"
	}
}

func (t *SQLTab) loadSchemasAsync() {
	go func() {
		_, cfg, err := t.sqlCfg()
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, t.w) })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()

		schemas, err := t.repo.Schemas(ctx, cfg)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, t.w) })
			return
		}
		sort.Strings(schemas)

		fyne.Do(func() {
			if !t.connected {
				return
			}
			t.allSchemas = schemas
			t.tschema.SetOptions(schemas)

			// We don't want to trigger extra queries by auto-picking the first schema.
			// Only preselect when we have a saved preference or a known driver default.
			prefSchema := strings.TrimSpace(fyne.CurrentApp().Preferences().String(prefSQLSchema))
			if prefSchema != "" && contains(schemas, prefSchema) {
				t.tschema.SetSelected(prefSchema)
				// OnChanged will fire and load tables.
				if t.tschema.OnChanged != nil {
					t.tschema.OnChanged(prefSchema)
				}
				return
			}

			def := t.defaultSchemaForDriver()
			if def != "" && contains(schemas, def) {
				t.tschema.SetSelected(def)
				if t.tschema.OnChanged != nil {
					t.tschema.OnChanged(def)
				}
				return
			}

			// Otherwise keep empty selection.
			t.tschema.SetSelected("")
			t.ttable.SetOptions(nil)
			t.ttable.SetSelected("")
			t.ttable.Disable()
			t.tcolumn.SetOptions(nil)
			t.tcolumn.SetSelected("")
			t.tcolumn.Disable()
		})
	}()
}

func (t *SQLTab) loadTablesAndColumnsAsync(schema, preferTable string) {
	go func() {
		_, cfg, err := t.sqlCfg()
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, t.w) })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()

		tables, err := t.repo.Tables(ctx, cfg, schema)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, t.w) })
			return
		}
		sort.Strings(tables)

		fyne.Do(func() {
			if !t.connected {
				return
			}
			t.allTables = tables
			t.ttable.SetOptions(tables)
			t.ttable.Enable()

			t.allColumns = nil
			t.tcolumn.SetOptions(nil)
			t.tcolumn.SetSelected("")
			t.tcolumn.Disable()

			// Do NOT auto-select the first table.
			// Only preselect when a preferred value is provided and exists.
			if preferTable != "" && contains(tables, preferTable) {
				t.ttable.SetSelected(preferTable)
				if t.ttable.OnChanged != nil {
					t.ttable.OnChanged(preferTable)
				}
			} else {
				t.ttable.SetSelected("")
			}
		})
	}()
}

func (t *SQLTab) loadColumnsAsync(schema, table, preferColumn string) {
	go func() {
		_, cfg, err := t.sqlCfg()
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, t.w) })
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()

		cols, err := t.repo.Columns(ctx, cfg, schema, table)
		if err != nil {
			fyne.Do(func() { dialog.ShowError(err, t.w) })
			return
		}
		sort.Strings(cols)

		fyne.Do(func() {
			if !t.connected {
				return
			}
			t.allColumns = cols
			t.tcolumn.SetOptions(cols)
			t.tcolumn.Enable()

			// Update WHERE field dropdowns (columns list)
			for _, r := range t.whereRows {
				if r != nil && r.fieldSel != nil {
					r.fieldSel.SetOptions(cols)
				}
			}

			// Do NOT auto-select the first column.
			if preferColumn != "" && contains(cols, preferColumn) {
				t.tcolumn.SetSelected(preferColumn)
			} else {
				t.tcolumn.SetSelected("")
			}
		})
	}()
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func (t *SQLTab) updateConnectButton() {
	if t.btnWrap == nil {
		return
	}
	connectBlue := color.NRGBA{R: 0x2d, G: 0x8c, B: 0xff, A: 0xff}
	disconnectRed := color.NRGBA{R: 0x7c, G: 0x0a, B: 0x02, A: 0xff}
	if t.connected {
		t.btnWrap.SetText("Disconnect")
		t.btnWrap.SetBackground(disconnectRed)
	} else {
		t.btnWrap.SetText("Connect")
		t.btnWrap.SetBackground(connectBlue)
	}
	t.btnWrap.Refresh()
}

func (t *SQLTab) Fetch(ctx context.Context) ([]byte, error) {
	vals, err := t.FetchMany(ctx)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("no rows")
	}
	return vals[0], nil
}

// FetchMany returns one or many raw values from the selected column (LIMIT 1 row).
// For regular blobs it returns a slice with one element; for Postgres bytea[] it returns all elements.
func (t *SQLTab) FetchMany(ctx context.Context) ([][]byte, error) {
	if !t.connected {
		return nil, fmt.Errorf("not connected")
	}

	schema := strings.TrimSpace(t.tschema.Selected())
	table := strings.TrimSpace(t.ttable.Selected())
	column := strings.TrimSpace(t.tcolumn.Selected())
	if schema == "" || table == "" || column == "" {
		return nil, fmt.Errorf("schema, table and column are required")
	}

	driverID, metaCfg, err := t.sqlCfg()
	if err != nil {
		return nil, err
	}

	repo, err := sqldata.New(driverID)
	if err != nil {
		return nil, err
	}

	join := "AND"
	if t.whereJoin != nil && strings.TrimSpace(t.whereJoin.Selected) != "" {
		join = strings.ToUpper(strings.TrimSpace(t.whereJoin.Selected))
		if join != "AND" && join != "OR" {
			join = "AND"
		}
	}

	conds := make([]sqldata.WhereCond, 0, len(t.whereRows))
	for _, r := range t.whereRows {
		if r == nil {
			continue
		}
		field := ""
		value := ""
		op := "="
		if r.fieldSel != nil {
			field = strings.TrimSpace(r.fieldSel.Selected())
		}
		if r.value != nil {
			value = strings.TrimSpace(r.value.Text)
		}
		if r.op != nil && strings.TrimSpace(r.op.Selected) != "" {
			op = strings.TrimSpace(r.op.Selected)
		}

		if field == "" || value == "" {
			continue
		}
		conds = append(conds, sqldata.WhereCond{Field: field, Op: op, Value: value})
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	vals, err := repo.FetchOneRaw(ctx, sqldata.Config{
		Host:     metaCfg.Host,
		Port:     metaCfg.Port,
		TLS:      metaCfg.TLS,
		User:     metaCfg.User,
		Password: metaCfg.Password,
		DB:       metaCfg.DB,
	}, sqldata.FetchOneRequest{
		Schema:    schema,
		Table:     table,
		Column:    column,
		WhereJoin: join,
		Where:     conds,
	})
	if err != nil {
		return nil, err
	}
	return vals, nil
}
