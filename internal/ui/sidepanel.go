package ui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/i4erkasov/proto-viewer/internal/domain"
	"github.com/i4erkasov/proto-viewer/internal/infrastructure/perf"
	"github.com/i4erkasov/proto-viewer/internal/ui/tab"
	"github.com/i4erkasov/proto-viewer/internal/ui/widgets/jsonviewer"
)

// sidePanel — одна «сторона» в режиме Diff: выбор источника (File/Redis),
// своя кнопка Decode и свой JSON-вьювер. Proto root/file/type берутся общие
// (сверху) через getProto. Переиспользует те же компоненты, что и обычный режим.
type sidePanel struct {
	w   fyne.Window
	dec domain.Decoder

	getProto func() (root, file, typ string, gzip bool)

	fileTab  *tab.FileTab
	redisTab *tab.RedisTab
	tabs     *container.AppTabs

	viewer *jsonviewer.JSONView
	status *widget.Label

	root fyne.CanvasObject
	json string // последний декодированный JSON

	onDecoded func() // вызывается после успешного декода (для пересчёта diff)
}

func newSidePanel(w fyne.Window, deps Deps, title string, getProto func() (string, string, string, bool)) *sidePanel {
	s := &sidePanel{
		w:        w,
		dec:      deps.Decoder,
		getProto: getProto,
	}

	s.fileTab = tab.NewTabFile(w, deps.FileRepo)
	s.redisTab = tab.NewTabRedis(w, deps.RedisRepo)
	s.tabs = container.NewAppTabs(
		container.NewTabItem(s.fileTab.Title(), container.NewBorder(s.fileTab.View(), nil, nil, nil, nil)),
		container.NewTabItem(s.redisTab.Title(), container.NewBorder(s.redisTab.View(), nil, nil, nil, nil)),
	)

	s.viewer = jsonviewer.NewJSONMarkdownView(w)
	s.viewer.SetOnlyMatchesVisible(false) // в Diff чекбокс «Only matches» не нужен
	s.status = widget.NewLabel("idle")

	// Низ: заголовок стороны + статус (декод запускается общей кнопкой Compare).
	footer := container.NewBorder(
		nil, nil,
		widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil,
		s.status,
	)
	// Центр — вьювер; панель поиска сверху, прижата к правому краю (скрыта по умолчанию).
	searchRow := container.NewHBox(layout.NewSpacer(), s.viewer.SearchBar())
	center := container.NewBorder(searchRow, nil, nil, nil, s.viewer.View())
	// Сверху — выбор источника, центр — вьювер, снизу — действия.
	s.root = container.NewBorder(s.tabs, footer, nil, nil, center)
	return s
}

// Decode декодирует текущий выбранный источник стороны.
func (s *sidePanel) Decode() { s.decode() }

// SetSearchVisible показывает/скрывает поиск во вьювере стороны.
func (s *sidePanel) SetSearchVisible(v bool) { s.viewer.SetSearchVisible(v) }

// SearchVisible сообщает, открыт ли поиск.
func (s *sidePanel) SearchVisible() bool { return s.viewer.SearchVisible() }

// FocusSearch ставит фокус в поле поиска стороны.
func (s *sidePanel) FocusSearch() {
	if s.w != nil {
		s.w.Canvas().Focus(s.viewer.SearchEntry())
	}
}

// View возвращает корневой объект стороны.
func (s *sidePanel) View() fyne.CanvasObject { return s.root }

// SetSourceVisible показывает/скрывает область выбора источника (File/Redis).
func (s *sidePanel) SetSourceVisible(v bool) {
	if v {
		s.tabs.Show()
	} else {
		s.tabs.Hide()
	}
	s.root.Refresh()
}

// DropFile переключает сторону на вкладку File и подставляет путь (для drag&drop).
func (s *sidePanel) DropFile(p string) {
	s.tabs.SelectIndex(0)
	s.fileTab.SetFilePath(p)
	s.fileTab.FlashDropHighlight()
}

func (s *sidePanel) decode() {
	root, file, typ, gzip := s.getProto()
	if root == "" || file == "" || typ == "" {
		s.status.SetText("set proto root/file/type")
		return
	}

	var src interface {
		Fetch(context.Context) ([]byte, error)
	}
	switch s.tabs.SelectedIndex() {
	case 0:
		src = s.fileTab
	case 1:
		src = s.redisTab
		if gz, ok := any(s.redisTab).(interface{ Gzip() bool }); ok {
			gzip = gz.Gzip()
		}
	default:
		src = s.fileTab
	}

	s.status.SetText("decoding…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		payload, err := src.Fetch(ctx)
		if err != nil {
			perf.Log("diff fetch error: %v", err)
			fyne.Do(func() {
				s.status.SetText("error")
				dialog.ShowError(err, s.w)
			})
			return
		}
		res, err := s.dec.Decode(ctx, domain.DecodeRequest{
			ProtoRoot: root,
			ProtoFile: file,
			FullType:  typ,
			Gzip:      gzip,
			Format:    domain.OutputFormatJSON,
			Bytes:     payload,
		})
		if err != nil {
			perf.Log("diff decode error: %v", err)
			fyne.Do(func() {
				s.status.SetText("error")
				dialog.ShowError(err, s.w)
			})
			return
		}
		fyne.Do(func() {
			s.json = strings.TrimSpace(res.Raw)
			s.viewer.SetJSON(s.json)
			s.status.SetText("OK")
			if s.onDecoded != nil {
				s.onDecoded()
			}
		})
	}()
}
