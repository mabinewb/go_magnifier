package ui

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	. "github.com/lxn/walk/declarative"

	"gomagnifier/internal/capture"
	"gomagnifier/internal/model"
	"gomagnifier/internal/persist"
	"gomagnifier/internal/winutil"
	)

const appName = "GoMagnifier"

const (
	mainWindowFixedWidth  = 680
	mainWindowFixedHeight = 560
	sourceConfigPanelHeight = 252
	mainFormLabelWidth      = 84
	mainValueLabelWidth     = 52
	statusFieldLabelWidth   = 40
)

const helpText = "Go Magnifier는 Go로 만든 Windows 전용 화면 확대 오버레이 프로그램입니다.\r\n\r\n1. 오버레이를 추가한 뒤 화면 캡처, 이미지 파일, 텍스트 중 하나를 소스로 선택합니다.\r\n2. 선택한 오버레이 표시 설정에서 배율과 투명도를 조절합니다. 리프레시율은 화면 캡처 소스에서만 사용됩니다.\r\n3. 텍스트 소스에서는 내용, 글자 크기, 굵게, 기울임, 글자색, 배경색, 외곽선 설정을 조절할 수 있습니다.\r\n4. 오버레이 창은 직접 드래그해 이동할 수 있고, 가장자리를 잡아 크기를 조절할 수 있습니다.\r\n5. 변경 사항은 기본 설정 파일(settings.json)에 자동 저장되며, 필요할 때만 별도 파일로 저장하거나 불러올 수 있습니다."

var sourceKindOptions = []string{"화면 캡처", "이미지 파일", "텍스트"}
var captureBackendOptions = []string{"자동", "기존 GDI BitBlt", "Desktop Duplication (DXGI)"}
var textFontOptions = loadSystemFontOptions()
var textAlignXOptions = []string{"왼쪽", "가운데", "오른쪽"}
var textAlignYOptions = []string{"위", "가운데", "아래"}

type overlayEntry struct {
	profile model.Profile
	overlay *OverlayWindow
	capture *capture.Service
	actualCaptureBackend string
}

type controller struct {
	store      *persist.Store
	mainWindow *walk.MainWindow
	overlays   []*overlayEntry

	activeIndex         int
	globalClickThrough bool
	minimizeToTray     bool
	alwaysOnTop        bool
	mainWindowRect     model.Rect
	exitRequested      bool

	overlayPicker   *walk.ComboBox
	sourceKindPicker *walk.ComboBox
	captureBackendPicker *walk.ComboBox
	sourceLabel     *walk.Label
	captureLabel    *walk.Label
	overlayLabel    *walk.Label
	opacityLabel    *walk.Label
	zoomXLabel      *walk.Label
	zoomYLabel      *walk.Label
	zoomXTitleLabel *walk.Label
	zoomXCurrentLabel *walk.Label
	zoomYTitleLabel *walk.Label
	zoomYCurrentLabel *walk.Label
	refreshTitleLabel *walk.Label
	refreshCurrentTitleLabel *walk.Label
	refreshLabel    *walk.Label
	textContentEdit *walk.TextEdit
	textFontSizeEdit *walk.LineEdit
	textFontFamilyPicker *walk.ComboBox
	textAlignXPicker *walk.ComboBox
	textAlignYPicker *walk.ComboBox
	textColorEdit   *walk.LineEdit
	textBackgroundEdit *walk.LineEdit
	textBackgroundAlphaSlider *walk.Slider
	textBackgroundAlphaLabel *walk.Label
	settingsPathLabel *walk.LineEdit
	statusLabel     *walk.LineEdit
	zoomXEdit       *walk.LineEdit
	zoomYEdit       *walk.LineEdit
	clickThroughBox *walk.CheckBox
	minimizeToTrayBox *walk.CheckBox
	alwaysOnTopBox  *walk.CheckBox
	aspectLockBox   *walk.CheckBox
	recursiveBlockBox *walk.CheckBox
	textBoldBox     *walk.CheckBox
	textItalicBox   *walk.CheckBox
	textShadowBox   *walk.CheckBox
	textShadowColorEdit *walk.LineEdit
	textShadowOffsetEdit *walk.LineEdit
	opacitySlider   *walk.Slider
	refreshSlider   *walk.Slider
	screenSourceBox *walk.GroupBox
	imageSourceBox  *walk.GroupBox
	textSourceBox   *walk.GroupBox
	syncingControls bool
	nextOverlaySeed int64
	trayIcon        *walk.NotifyIcon
	appIcon         *walk.Icon
	lastTrayLeftClick time.Time
}

func Run() error {
	store, err := persist.NewStore(appName)
	if err != nil {
		return err
	}
	appendRuntimeLog("INFO", "application start")

	ctrl := &controller{store: store}
	if err := ctrl.createMainWindow(); err != nil {
		appendRuntimeLog("ERROR", "main window creation failed: "+err.Error())
		return err
	}

	session, err := store.LoadSettings()
	if err != nil {
		appendRuntimeLog("ERROR", "settings load failed: "+err.Error())
		if ctrl.mainWindow != nil {
			ctrl.mainWindow.Dispose()
		}
		return err
	}
	if len(session.Overlays) == 0 {
		session = model.Session{Overlays: []model.Profile{model.DefaultProfile(virtualScreenBounds())}}
	}
	if err := ctrl.loadSession(session); err != nil {
		appendRuntimeLog("ERROR", "session load failed: "+err.Error())
		if ctrl.mainWindow != nil {
			ctrl.mainWindow.Dispose()
		}
		return err
	}
	ctrl.syncControlsFromActive()
	ctrl.updateLabels()
	ctrl.enforceMainWindowFixedSize()
	ctrl.mainWindow.Show()
	ctrl.mainWindow.Run()
	return nil
}

func (c *controller) createMainWindow() error {
	window := MainWindow{
		AssignTo: &c.mainWindow,
		Title:    "Go Magnifier",
		MinSize:  Size{Width: mainWindowFixedWidth, Height: mainWindowFixedHeight},
		MaxSize:  Size{Width: mainWindowFixedWidth, Height: mainWindowFixedHeight},
		Size:     Size{Width: mainWindowFixedWidth, Height: mainWindowFixedHeight},
		Layout: VBox{
			Margins: Margins{Left: 6, Top: 6, Right: 6, Bottom: 6},
			Spacing: 6,
		},
		Children: []Widget{
			Composite{
				StretchFactor: 0,
				Layout: VBox{MarginsZero: true, Spacing: 4},
				Children: []Widget{
					Composite{
						Layout: HBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{Text: "변경 사항은 settings.json에 자동 저장됩니다."},
							HSpacer{},
							PushButton{Text: "도움말", MinSize: Size{Width: 72, Height: 24}, MaxSize: Size{Width: 72, Height: 24}, OnClicked: c.showHelp},
						},
					},
					Composite{
						Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6},
						Children: []Widget{
							Label{Text: "설정 경로", MinSize: Size{Width: mainFormLabelWidth}},
							LineEdit{AssignTo: &c.settingsPathLabel, ReadOnly: true, Text: c.settingsPathText(), MinSize: Size{Width: 320, Height: 20}},
						},
					},
					Composite{
						Layout: Grid{Columns: 3, MarginsZero: true, Spacing: 4},
						Children: []Widget{
							PushButton{Text: "폴더 열기", OnClicked: c.openSettingsFolder},
							PushButton{Text: "저장", OnClicked: c.saveSettingsAs},
							PushButton{Text: "불러오기", OnClicked: c.loadSettingsFromFile},
						},
					},
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 2},
						Children: []Widget{
							Label{Text: "앱 옵션"},
							Composite{
								Layout: HBox{MarginsZero: true, Spacing: 10},
								Children: []Widget{
									CheckBox{AssignTo: &c.clickThroughBox, Text: "마우스 클릭 통과 허용", OnCheckedChanged: c.clickThroughChanged},
									CheckBox{AssignTo: &c.minimizeToTrayBox, Text: "트레이로 최소화", OnCheckedChanged: c.minimizeToTrayChanged},
									CheckBox{AssignTo: &c.alwaysOnTopBox, Text: "설정창 항상 위에 표시", OnCheckedChanged: c.alwaysOnTopChanged},
								},
							},
						},
					},
				},
			},
			Composite{
				Layout: HBox{MarginsZero: true, Spacing: 6},
				Children: []Widget{
					Composite{
						Layout: VBox{MarginsZero: true, Spacing: 6},
						Children: []Widget{
							GroupBox{
								Title:  "오버레이 관리",
								Layout: VBox{Margins: Margins{Left: 6, Top: 6, Right: 6, Bottom: 6}, Spacing: 4},
								Children: []Widget{
									Composite{
										Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6},
										Children: []Widget{
											Label{Text: "편집할 오버레이", MinSize: Size{Width: mainFormLabelWidth}},
											ComboBox{AssignTo: &c.overlayPicker, OnCurrentIndexChanged: c.overlaySelectionChanged},
										},
									},
									Composite{
										Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 4},
										Children: []Widget{
											PushButton{Text: "오버레이 추가", OnClicked: c.addOverlay},
											PushButton{Text: "선택 오버레이 제거", OnClicked: c.removeActiveOverlay},
										},
									},
									Label{AssignTo: &c.overlayLabel},
								},
							},
							Composite{
								Layout: VBox{MarginsZero: true, Spacing: 4},
								Children: []Widget{
									Label{Text: "선택한 오버레이의 표시 설정"},
									Composite{
										Layout: Grid{Columns: 4, MarginsZero: true, Spacing: 6},
										Children: []Widget{
											Label{AssignTo: &c.zoomXTitleLabel, Text: "X축 배율 (%)", MinSize: Size{Width: mainFormLabelWidth}},
											LineEdit{AssignTo: &c.zoomXEdit, OnEditingFinished: c.zoomXChanged},
											Label{AssignTo: &c.zoomXCurrentLabel, Text: "현재 값", MinSize: Size{Width: mainValueLabelWidth}},
											Label{AssignTo: &c.zoomXLabel},
											Label{AssignTo: &c.zoomYTitleLabel, Text: "Y축 배율 (%)", MinSize: Size{Width: mainFormLabelWidth}},
											LineEdit{AssignTo: &c.zoomYEdit, OnEditingFinished: c.zoomYChanged},
											Label{AssignTo: &c.zoomYCurrentLabel, Text: "현재 값", MinSize: Size{Width: mainValueLabelWidth}},
											Label{AssignTo: &c.zoomYLabel},
											Label{Text: "투명도", MinSize: Size{Width: mainFormLabelWidth}},
											Slider{AssignTo: &c.opacitySlider, MinValue: 0, MaxValue: 100, OnValueChanged: c.opacityChanged},
											Label{Text: "현재 값", MinSize: Size{Width: mainValueLabelWidth}},
											Label{AssignTo: &c.opacityLabel, MinSize: Size{Width: 40}},
											Label{AssignTo: &c.refreshTitleLabel, Text: "리프레시율", MinSize: Size{Width: mainFormLabelWidth}},
											Slider{AssignTo: &c.refreshSlider, MinValue: 1, MaxValue: 60, OnValueChanged: c.refreshChanged},
											Label{AssignTo: &c.refreshCurrentTitleLabel, Text: "현재 값", MinSize: Size{Width: mainValueLabelWidth}},
											Label{AssignTo: &c.refreshLabel},
										},
									},
									CheckBox{AssignTo: &c.aspectLockBox, Text: "오버레이 비율이 소스 비율에 맞춰지도록 유지", OnCheckedChanged: c.aspectLockChanged},
									CheckBox{AssignTo: &c.recursiveBlockBox, Text: "재귀 캡처 차단 (겹치는 오버레이는 캡처할 때 뒤 화면이 보이도록 처리)", OnCheckedChanged: c.recursiveBlockChanged},
								},
							},
						},
					},
					GroupBox{
						Title:  "소스",
						Layout: VBox{Margins: Margins{Left: 6, Top: 6, Right: 6, Bottom: 6}, Spacing: 4},
						Children: []Widget{
							Composite{
								Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6},
								Children: []Widget{
									Label{Text: "사용할 소스", MinSize: Size{Width: mainFormLabelWidth}},
									ComboBox{AssignTo: &c.sourceKindPicker, Model: sourceKindOptions, OnCurrentIndexChanged: c.sourceKindChanged},
								},
							},
							Composite{
								MinSize: Size{Height: sourceConfigPanelHeight},
								MaxSize: Size{Width: 4096, Height: sourceConfigPanelHeight},
								Layout: VBox{MarginsZero: true, Spacing: 4},
								Children: []Widget{
									GroupBox{
										AssignTo: &c.screenSourceBox,
										Title:  "화면 캡처 설정",
										Layout: VBox{Margins: Margins{Left: 4, Top: 4, Right: 4, Bottom: 4}, Spacing: 4},
										Children: []Widget{
											Composite{
												Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6},
												Children: []Widget{
													Label{Text: "캡처 백엔드", MinSize: Size{Width: mainFormLabelWidth}},
													ComboBox{AssignTo: &c.captureBackendPicker, Model: captureBackendOptions, OnCurrentIndexChanged: c.captureBackendChanged},
												},
											},
											PushButton{Text: "화면 캡처 영역 지정", OnClicked: c.selectCaptureArea},
										},
									},
									GroupBox{
										AssignTo: &c.imageSourceBox,
										Title:  "이미지 설정",
										Layout: VBox{Margins: Margins{Left: 4, Top: 4, Right: 4, Bottom: 4}, Spacing: 4},
										Children: []Widget{
											PushButton{Text: "이미지 파일 선택", OnClicked: c.selectImageFile},
										},
									},
									GroupBox{
										AssignTo: &c.textSourceBox,
										Title:  "텍스트 서식",
										Layout: VBox{Margins: Margins{Left: 4, Top: 8, Right: 4, Bottom: 4}, Spacing: 4},
										Children: []Widget{
											TextEdit{AssignTo: &c.textContentEdit, Text: "", MinSize: Size{Width: 320, Height: 52}, OnTextChanged: c.textContentChanged, OnKeyUp: func(walk.Key) { c.textContentChanged() }},
											Composite{
												Layout: Grid{Columns: 4, MarginsZero: true, Spacing: 6},
												Children: []Widget{
													Label{Text: "폰트", MinSize: Size{Width: mainFormLabelWidth}},
													ComboBox{AssignTo: &c.textFontFamilyPicker, Model: textFontOptions, OnCurrentIndexChanged: c.textFontFamilyChanged},
													Label{Text: "글자 크기", MinSize: Size{Width: mainFormLabelWidth}},
													LineEdit{AssignTo: &c.textFontSizeEdit, OnEditingFinished: c.textFontSizeChanged},
													Label{Text: "가로 정렬", MinSize: Size{Width: mainFormLabelWidth}},
													ComboBox{AssignTo: &c.textAlignXPicker, Model: textAlignXOptions, OnCurrentIndexChanged: c.textAlignXChanged},
													Label{Text: "세로 정렬", MinSize: Size{Width: mainFormLabelWidth}},
													ComboBox{AssignTo: &c.textAlignYPicker, Model: textAlignYOptions, OnCurrentIndexChanged: c.textAlignYChanged},
													Label{Text: "글자색", MinSize: Size{Width: mainFormLabelWidth}},
													LineEdit{AssignTo: &c.textColorEdit, OnEditingFinished: c.textColorChanged},
													Label{Text: "배경색", MinSize: Size{Width: mainFormLabelWidth}},
													LineEdit{AssignTo: &c.textBackgroundEdit, OnEditingFinished: c.textBackgroundChanged},
													Label{Text: "외곽선 색", MinSize: Size{Width: mainFormLabelWidth}},
													LineEdit{AssignTo: &c.textShadowColorEdit, OnEditingFinished: c.textShadowColorChanged},
													Label{Text: "외곽선 두께", MinSize: Size{Width: mainFormLabelWidth}},
													LineEdit{AssignTo: &c.textShadowOffsetEdit, OnEditingFinished: c.textShadowOffsetChanged},
												},
											},
											Composite{
												Layout: Grid{Columns: 4, MarginsZero: true, Spacing: 6},
												Children: []Widget{
													Label{Text: "배경 알파", MinSize: Size{Width: mainFormLabelWidth}},
													Slider{AssignTo: &c.textBackgroundAlphaSlider, MinValue: 0, MaxValue: 100, OnValueChanged: c.textBackgroundAlphaChanged},
													Label{Text: "현재 값", MinSize: Size{Width: mainValueLabelWidth}},
													Label{AssignTo: &c.textBackgroundAlphaLabel, MinSize: Size{Width: 40}},
												},
											},
											Composite{
												Layout: HBox{MarginsZero: true, Spacing: 4},
												Children: []Widget{
													CheckBox{AssignTo: &c.textBoldBox, Text: "굵게", OnCheckedChanged: c.textBoldChanged},
													CheckBox{AssignTo: &c.textItalicBox, Text: "기울임", OnCheckedChanged: c.textItalicChanged},
													CheckBox{AssignTo: &c.textShadowBox, Text: "텍스트 외곽선", OnCheckedChanged: c.textShadowChanged},
													Label{Text: "색상 형식: #RRGGBB"},
												},
											},
										},
									},
								},
							},
							Label{AssignTo: &c.sourceLabel},
							Label{AssignTo: &c.captureLabel},
						},
					},
				},
			},
			Composite{
				StretchFactor: 0,
				Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 6},
				Children: []Widget{
					Label{Text: "상태", MinSize: Size{Width: statusFieldLabelWidth, Height: 20}},
					LineEdit{AssignTo: &c.statusLabel, ReadOnly: true, Text: "프로그램을 사용할 준비가 되었습니다.", MinSize: Size{Width: 300, Height: 20}},
				},
			},
		},
	}
	if err := window.Create(); err != nil {
		return err
	}
	disableMainWindowMaximize(c.mainWindow.Handle())
	if err := c.initializeShellIntegration(); err != nil {
		if c.mainWindow != nil {
			c.mainWindow.Dispose()
		}
		return err
	}
	c.captureMainWindowRect()
	return nil
}

func disableMainWindowMaximize(hwnd win.HWND) {
	if hwnd == 0 {
		return
	}
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	style &^= win.WS_MAXIMIZEBOX | win.WS_THICKFRAME
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(style))
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

func (c *controller) showHelp() {
	if c.mainWindow == nil {
		return
	}
	walk.MsgBox(c.mainWindow, "Go Magnifier 도움말", helpText, walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

func (c *controller) loadSession(session model.Session) error {
	c.disposeOverlays()
	c.overlays = nil
	c.activeIndex = 0
	c.globalClickThrough = sessionGlobalClickThrough(session)
	c.minimizeToTray = session.MinimizeToTray
	c.alwaysOnTop = session.AlwaysOnTop
	fallbackRect := model.Rect{Width: mainWindowFixedWidth, Height: mainWindowFixedHeight}
	if c.mainWindow != nil {
		bounds := c.mainWindow.Bounds()
		fallbackRect = model.Rect{X: bounds.X, Y: bounds.Y, Width: mainWindowFixedWidth, Height: mainWindowFixedHeight}
	}
	c.mainWindowRect = sessionMainWindowRect(session, fallbackRect)
	c.mainWindowRect.Width = mainWindowFixedWidth
	c.mainWindowRect.Height = mainWindowFixedHeight
	if !c.mainWindowRect.Empty() && c.mainWindow != nil {
		_ = c.mainWindow.SetBounds(toWalkRect(c.mainWindowRect))
	}

	for _, profile := range session.Overlays {
		profile.ClickThrough = c.globalClickThrough
		entry, err := c.createOverlayEntry(profile)
		if err != nil {
			c.disposeOverlays()
			return err
		}
		c.overlays = append(c.overlays, entry)
	}
	if len(c.overlays) == 0 {
		entry, err := c.createOverlayEntry(model.DefaultProfile(virtualScreenBounds()))
		if err != nil {
			return err
		}
		entry.profile.ClickThrough = c.globalClickThrough
		if entry.overlay != nil {
			_ = entry.overlay.ApplyProfile(entry.profile)
		}
		c.overlays = append(c.overlays, entry)
	}
	if session.ActiveOverlayID != "" {
		for index, entry := range c.overlays {
			if entry.profile.ID == session.ActiveOverlayID {
				c.activeIndex = index
				break
			}
		}
	}
	c.refreshOverlayPicker()
	c.applyMainWindowOptions()
	return nil
}

func (c *controller) createOverlayEntry(profile model.Profile) (*overlayEntry, error) {
	profile = profile.Sanitized()
	if profile.ID == "" {
		profile.ID = c.newOverlayID()
	}
	profile.ClickThrough = c.globalClickThrough
	if profile.SourceKind == "" {
		profile.SourceKind = model.SourceScreen
	}
	if profile.SourceKind == model.SourceImage {
		if err := syncImageCaptureRect(&profile); err != nil {
			return nil, err
		}
	}
	if profile.SourceKind == model.SourceText {
		syncTextCaptureRect(&profile)
	}

	window, err := NewOverlayWindow(profile)
	if err != nil {
		return nil, err
	}
	entry := &overlayEntry{profile: profile, overlay: window}
	window.SetBoundsChanged(func(rect model.Rect, final bool) {
		c.overlayBoundsChanged(profile.ID, rect, final)
	})
	window.SetPresentErrorHandler(func(err error) {
		if err == nil || c.mainWindow == nil {
			return
		}
		c.mainWindow.Synchronize(func() {
			c.setStatus(err.Error())
		})
	})
	entry.capture = capture.NewService(window.SubmitFrame, func(err error) {
		if err == nil || c.mainWindow == nil {
			return
		}
		c.mainWindow.Synchronize(func() {
			c.setStatus("소스 갱신 오류: " + err.Error())
		})
	}, func(actualBackend string) {
		if c.mainWindow == nil {
			entry.actualCaptureBackend = actualBackend
			c.updateRecursiveCaptureState(entry)
			return
		}
		c.mainWindow.Synchronize(func() {
			entry.actualCaptureBackend = actualBackend
			c.updateRecursiveCaptureState(entry)
			c.syncControlsFromActive()
			c.updateLabels()
		})
	})
	entry.capture.Start(profile)
	return entry, nil
}

func (c *controller) disposeOverlays() {
	for _, entry := range c.overlays {
		if entry.capture != nil {
			entry.capture.Stop()
		}
		if entry.overlay != nil {
			entry.overlay.PrepareForClose()
			entry.overlay.Dispose()
			entry.overlay = nil
		}
	}
}

func (c *controller) activeEntry() *overlayEntry {
	if c.activeIndex < 0 || c.activeIndex >= len(c.overlays) {
		return nil
	}
	return c.overlays[c.activeIndex]
}

func (c *controller) refreshOverlayPicker() {
	if c.overlayPicker == nil {
		return
	}
	names := make([]string, 0, len(c.overlays))
	for index, entry := range c.overlays {
		source := "화면"
		if entry.profile.SourceKind == model.SourceImage {
			source = "이미지"
		} else if entry.profile.SourceKind == model.SourceText {
			source = "텍스트"
		}
		names = append(names, fmt.Sprintf("오버레이 %d · %s", index+1, source))
	}
	c.syncingControls = true
	c.overlayPicker.SetModel(names)
	if len(names) == 0 {
		c.overlayPicker.SetCurrentIndex(-1)
	} else {
		if c.activeIndex < 0 || c.activeIndex >= len(names) {
			c.activeIndex = 0
		}
		c.overlayPicker.SetCurrentIndex(c.activeIndex)
	}
	c.syncingControls = false
}

func (c *controller) overlaySelectionChanged() {
	if c.syncingControls || c.overlayPicker == nil {
		return
	}
	index := c.overlayPicker.CurrentIndex()
	if index < 0 || index >= len(c.overlays) {
		return
	}
	c.activeIndex = index
	c.syncControlsFromActive()
	c.updateLabels()
}

func (c *controller) addOverlay() {
	profile := model.DefaultProfile(virtualScreenBounds())
	if current := c.activeEntry(); current != nil {
		profile = current.profile
		profile.ID = ""
		profile.OverlayRect.X += 40
		profile.OverlayRect.Y += 40
	}
	profile.ClickThrough = c.globalClickThrough
	entry, err := c.createOverlayEntry(profile)
	if err != nil {
		c.setStatus("오버레이 생성 실패: " + err.Error())
		return
	}
	c.overlays = append(c.overlays, entry)
	c.activeIndex = len(c.overlays) - 1
	c.refreshOverlayPicker()
	c.syncControlsFromActive()
	c.updateLabels()
	c.saveSettings()
	c.setStatus("새 오버레이를 추가했습니다.")
}

func (c *controller) removeActiveOverlay() {
	if len(c.overlays) <= 1 {
		c.setStatus("오버레이는 최소 1개가 필요합니다.")
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	if entry.capture != nil {
		entry.capture.Stop()
	}
	if entry.overlay != nil {
		entry.overlay.Dispose()
	}
	c.overlays = append(c.overlays[:c.activeIndex], c.overlays[c.activeIndex+1:]...)
	if c.activeIndex >= len(c.overlays) {
		c.activeIndex = len(c.overlays) - 1
	}
	c.refreshOverlayPicker()
	c.syncControlsFromActive()
	c.updateLabels()
	c.saveSettings()
	c.setStatus("선택한 오버레이를 제거했습니다.")
}

func (c *controller) selectCaptureArea() {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	rect, ok, err := SelectRect("캡처할 화면 영역 선택", entry.profile.CaptureRect)
	if err != nil {
		c.setStatus(err.Error())
		return
	}
	if !ok {
		c.setStatus("캡처 영역 선택이 취소되었습니다.")
		return
	}
	entry.profile.SourceKind = model.SourceScreen
	entry.profile.ImagePath = ""
	entry.profile.CaptureRect = rect
	c.applyZoomToOverlay(entry, "")
	c.applyActiveProfile(true)
	c.setStatus("캡처 영역을 업데이트했습니다.")
}

func (c *controller) selectImageFile() {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	dlg := new(walk.FileDialog)
	dlg.Title = "이미지 파일 선택"
	dlg.Filter = "Image Files (*.png;*.jpg;*.jpeg;*.gif)|*.png;*.jpg;*.jpeg;*.gif"
	ok, err := dlg.ShowOpen(c.mainWindow)
	if err != nil {
		c.setStatus("이미지 파일 선택 실패: " + err.Error())
		return
	}
	if !ok {
		return
	}
	entry.profile.SourceKind = model.SourceImage
	entry.profile.ImagePath = dlg.FilePath
	if err := syncImageCaptureRect(&entry.profile); err != nil {
		c.setStatus("이미지 정보 읽기 실패: " + err.Error())
		return
	}
	c.applyZoomToOverlay(entry, "")
	c.applyActiveProfile(true)
	c.setStatus("이미지 파일을 오버레이 소스로 설정했습니다.")
}

func (c *controller) switchToScreenSource() {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	entry.profile.SourceKind = model.SourceScreen
	entry.profile.ImagePath = ""
	entry.profile.LockAspect = true
	c.applyActiveProfile(true)
	c.setStatus("화면 캡처 소스로 전환했습니다.")
}

func (c *controller) switchToTextSource() {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	entry.profile.SourceKind = model.SourceText
	entry.profile.ImagePath = ""
	entry.profile.LockAspect = false
	syncTextCaptureRect(&entry.profile)
	entry.profile.ZoomX = 100
	entry.profile.ZoomY = 100
	c.applyActiveProfile(true)
	c.setStatus("텍스트 소스로 전환했습니다.")
}

func (c *controller) sourceKindChanged() {
	if c.syncingControls || c.sourceKindPicker == nil {
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	switch c.sourceKindPicker.CurrentIndex() {
	case 1:
		entry.profile.SourceKind = model.SourceImage
		entry.profile.LockAspect = true
		if entry.profile.ImagePath != "" {
			if err := syncImageCaptureRect(&entry.profile); err != nil {
				c.setStatus("이미지 정보 읽기 실패: " + err.Error())
			}
		}
		c.setStatus("이미지 소스로 전환했습니다. 필요하면 이미지 파일을 다시 선택하세요.")
	case 2:
		entry.profile.SourceKind = model.SourceText
		entry.profile.LockAspect = false
		syncTextCaptureRect(&entry.profile)
		entry.profile.ZoomX = 100
		entry.profile.ZoomY = 100
		c.setStatus("텍스트 소스로 전환했습니다.")
	default:
		entry.profile.SourceKind = model.SourceScreen
		entry.profile.LockAspect = true
		c.setStatus("화면 캡처 소스로 전환했습니다.")
	}
	c.applyActiveProfile(true)
}

func (c *controller) zoomXChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	value, err := parseZoomInput(c.zoomXEdit.Text())
	if err != nil {
		c.setStatus("X축 배율 입력이 올바르지 않습니다.")
		return
	}
	entry.profile.ZoomX = value
	c.applyZoomToOverlay(entry, "x")
	c.applyActiveProfile(true)
}

func (c *controller) zoomYChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	value, err := parseZoomInput(c.zoomYEdit.Text())
	if err != nil {
		c.setStatus("Y축 배율 입력이 올바르지 않습니다.")
		return
	}
	entry.profile.ZoomY = value
	c.applyZoomToOverlay(entry, "y")
	c.applyActiveProfile(true)
}

func (c *controller) opacityChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	entry.profile.Opacity = c.opacitySlider.Value()
	c.applyActiveProfile(true)
}

func (c *controller) refreshChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceScreen {
		return
	}
	entry.profile.RefreshRate = c.refreshSlider.Value()
	c.applyActiveProfile(true)
}

func (c *controller) captureBackendChanged() {
	if c.syncingControls || c.captureBackendPicker == nil {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceScreen {
		return
	}
	entry.profile.CaptureBackend = captureBackendValue(c.captureBackendPicker.CurrentIndex())
	c.applyActiveProfile(true)
	if entry.profile.CaptureBackend == model.CaptureBackendGDI {
		c.setStatus("화면 캡처 백엔드를 기존 GDI BitBlt로 변경했습니다.")
		return
	}
	if entry.profile.CaptureBackend == model.CaptureBackendDDA {
		c.setStatus("화면 캡처 백엔드를 Desktop Duplication (DXGI)로 변경했습니다.")
		return
	}
	c.setStatus("화면 캡처 백엔드를 자동으로 설정했습니다. Desktop Duplication (DXGI)을 우선 시도하고 실패하면 기존 GDI BitBlt로 대체합니다.")
}

func (c *controller) textContentChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textContentEdit == nil {
		return
	}
	entry.profile.TextContent = currentTextEditValue(c.textContentEdit)
	syncTextCaptureRect(&entry.profile)
	entry.profile = entry.profile.Sanitized()
	if entry.overlay != nil {
		_ = entry.overlay.ApplyProfile(entry.profile)
		if frame, err := capture.RenderTextFrame(entry.profile); err == nil {
			if err := entry.overlay.PresentFrame(frame); err != nil {
				c.setStatus("텍스트 표시 실패: " + err.Error())
			}
		} else {
			c.setStatus("텍스트 렌더링 실패: " + err.Error())
		}
		c.updateRecursiveCaptureState(entry)
	}
	if entry.capture != nil {
		entry.capture.UpdateProfile(entry.profile)
	}
	c.updateLabels()
	c.saveSettings()
}

func currentTextEditValue(widget *walk.TextEdit) string {
	if widget == nil {
		return ""
	}
	hwnd := widget.Handle()
	if hwnd == 0 {
		return widget.Text()
	}
	length := int(win.SendMessage(hwnd, win.WM_GETTEXTLENGTH, 0, 0))
	if length <= 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	if win.SendMessage(hwnd, win.WM_GETTEXT, uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0]))) == 0 {
		return widget.Text()
	}
	return syscall.UTF16ToString(buffer)
}

func (c *controller) textFontSizeChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textFontSizeEdit == nil {
		return
	}
	value, err := strconv.Atoi(strings.TrimSpace(c.textFontSizeEdit.Text()))
	if err != nil {
		c.setStatus("글자 크기는 숫자로 입력해야 합니다.")
		return
	}
	entry.profile.TextFontSize = value
	c.applyActiveProfile(true)
}

func (c *controller) textFontFamilyChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textFontFamilyPicker == nil {
		return
	}
	index := c.textFontFamilyPicker.CurrentIndex()
	if index < 0 || index >= len(textFontOptions) {
		return
	}
	entry.profile.TextFontFamily = textFontOptions[index]
	c.applyActiveProfile(true)
}

func (c *controller) textAlignXChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textAlignXPicker == nil {
		return
	}
	entry.profile.TextAlignX = alignXFromIndex(c.textAlignXPicker.CurrentIndex())
	c.applyActiveProfile(true)
}

func (c *controller) textAlignYChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textAlignYPicker == nil {
		return
	}
	entry.profile.TextAlignY = alignYFromIndex(c.textAlignYPicker.CurrentIndex())
	c.applyActiveProfile(true)
}

func (c *controller) textColorChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textColorEdit == nil {
		return
	}
	entry.profile.TextColor = strings.TrimSpace(c.textColorEdit.Text())
	c.applyActiveProfile(true)
}

func (c *controller) textBackgroundChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textBackgroundEdit == nil {
		return
	}
	entry.profile.TextBackground = strings.TrimSpace(c.textBackgroundEdit.Text())
	c.applyActiveProfile(true)
}

func (c *controller) textBackgroundAlphaChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textBackgroundAlphaSlider == nil {
		return
	}
	entry.profile.TextBackgroundAlpha = c.textBackgroundAlphaSlider.Value()
	c.applyActiveProfile(true)
}

func (c *controller) textBoldChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textBoldBox == nil {
		return
	}
	entry.profile.TextBold = c.textBoldBox.Checked()
	c.applyActiveProfile(true)
}

func (c *controller) textItalicChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textItalicBox == nil {
		return
	}
	entry.profile.TextItalic = c.textItalicBox.Checked()
	c.applyActiveProfile(true)
}

func (c *controller) textShadowChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textShadowBox == nil {
		return
	}
	entry.profile.TextShadow = c.textShadowBox.Checked()
	c.applyActiveProfile(true)
}

func (c *controller) textShadowColorChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textShadowColorEdit == nil {
		return
	}
	entry.profile.TextShadowColor = strings.TrimSpace(c.textShadowColorEdit.Text())
	c.applyActiveProfile(true)
}

func (c *controller) textShadowOffsetChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil || entry.profile.SourceKind != model.SourceText || c.textShadowOffsetEdit == nil {
		return
	}
	value, err := strconv.Atoi(strings.TrimSpace(c.textShadowOffsetEdit.Text()))
	if err != nil {
		c.setStatus("테두리 두께는 숫자로 입력해야 합니다.")
		return
	}
	entry.profile.TextShadowOffset = value
	c.applyActiveProfile(true)
}

func (c *controller) clickThroughChanged() {
	if c.syncingControls {
		return
	}
	c.globalClickThrough = c.clickThroughBox.Checked()
	for _, entry := range c.overlays {
		if entry == nil {
			continue
		}
		entry.profile.ClickThrough = c.globalClickThrough
		entry.profile = entry.profile.Sanitized()
		if entry.overlay != nil {
			_ = entry.overlay.ApplyProfile(entry.profile)
		}
		if entry.capture != nil {
			entry.capture.UpdateProfile(entry.profile)
		}
	}
	c.syncControlsFromActive()
	c.updateLabels()
	c.saveSettings()
}

func (c *controller) alwaysOnTopChanged() {
	if c.syncingControls {
		return
	}
	c.alwaysOnTop = c.alwaysOnTopBox.Checked()
	c.applyMainWindowOptions()
	c.saveSettings()
}

func (c *controller) minimizeToTrayChanged() {
	if c.syncingControls {
		return
	}
	c.minimizeToTray = c.minimizeToTrayBox.Checked()
	c.applyMainWindowOptions()
	c.saveSettings()
}

func (c *controller) aspectLockChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	entry.profile.LockAspect = c.aspectLockBox.Checked()
	c.applyZoomToOverlay(entry, "")
	c.applyActiveProfile(true)
}

func (c *controller) recursiveBlockChanged() {
	if c.syncingControls {
		return
	}
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	if !c.recursiveCaptureSupported(entry) {
		entry.profile.BlockRecursiveCapture = false
		if c.recursiveBlockBox != nil {
			c.syncingControls = true
			c.recursiveBlockBox.SetChecked(false)
			c.syncingControls = false
		}
		c.setStatus("재귀 캡처 차단은 Desktop Duplication (DXGI)이 실제로 사용 중일 때만 지원됩니다.")
		return
	}
	entry.profile.BlockRecursiveCapture = c.recursiveBlockBox.Checked()
	c.applyActiveProfile(true)
	if entry.profile.BlockRecursiveCapture {
		c.setStatus("현재 오버레이는 Desktop Duplication (DXGI) 백엔드에서 재귀 캡처 차단을 사용합니다.")
	} else {
		c.setStatus("이 오버레이는 일반 캡처 상태입니다. 자기 캡처와 겹치면 재귀 캡처가 발생할 수 있습니다.")
	}
}

func (c *controller) saveSettingsAs() {
	dlg := new(walk.FileDialog)
	dlg.Title = "설정 파일 저장"
	dlg.Filter = "JSON Files (*.json)|*.json"
	dlg.FilePath = "gomagnifier-settings.json"
	ok, err := dlg.ShowSave(c.mainWindow)
	if err != nil {
		c.setStatus("설정 파일 저장 대화상자 실패: " + err.Error())
		return
	}
	if !ok {
		return
	}
	if err := c.store.SaveSettingsAs(dlg.FilePath, c.currentSession()); err != nil {
		c.setStatus("설정 파일 저장 실패: " + err.Error())
		return
	}
	c.setStatus("설정 파일을 저장했습니다: " + dlg.FilePath)
}

func (c *controller) openSettingsFolder() {
	if c.store == nil {
		c.setStatus("설정 폴더 경로를 확인할 수 없습니다.")
		return
	}
	rootDir := c.store.RootDir()
	if strings.TrimSpace(rootDir) == "" {
		c.setStatus("설정 폴더 경로를 확인할 수 없습니다.")
		return
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		c.setStatus("설정 폴더 준비 실패: " + err.Error())
		return
	}
	if err := exec.Command("explorer.exe", rootDir).Start(); err != nil {
		c.setStatus("설정 폴더 열기 실패: " + err.Error())
		return
	}
	c.setStatus("설정 폴더를 열었습니다: " + rootDir)
}

func (c *controller) loadSettingsFromFile() {
	dlg := new(walk.FileDialog)
	dlg.Title = "설정 파일 불러오기"
	dlg.Filter = "JSON Files (*.json)|*.json"
	ok, err := dlg.ShowOpen(c.mainWindow)
	if err != nil {
		c.setStatus("설정 파일 불러오기 대화상자 실패: " + err.Error())
		return
	}
	if !ok {
		return
	}
	session, err := c.store.LoadSettingsFrom(dlg.FilePath)
	if err != nil {
		c.setStatus("설정 파일 불러오기 실패: " + err.Error())
		return
	}
	if len(session.Overlays) == 0 {
		c.setStatus("불러온 설정 파일에 오버레이가 없습니다.")
		return
	}
	if err := c.loadSession(session); err != nil {
		c.setStatus("설정 적용 실패: " + err.Error())
		return
	}
	c.syncControlsFromActive()
	c.updateLabels()
	c.saveSettings()
	c.setStatus("설정 파일을 불러왔습니다: " + dlg.FilePath)
}

func (c *controller) applyActiveProfile(save bool) {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	if entry.profile.SourceKind == model.SourceImage {
		if err := syncImageCaptureRect(&entry.profile); err != nil {
			c.setStatus("이미지 정보 읽기 실패: " + err.Error())
			return
		}
	}
	if entry.profile.SourceKind == model.SourceText {
		syncTextCaptureRect(&entry.profile)
	}
	entry.profile = entry.profile.Sanitized()
	if entry.overlay != nil {
		_ = entry.overlay.ApplyProfile(entry.profile)
		c.updateRecursiveCaptureState(entry)
	}
	if entry.capture != nil {
		entry.capture.UpdateProfile(entry.profile)
	}
	c.syncZoomFromOverlay(entry)
	c.syncControlsFromActive()
	c.updateLabels()
	if save {
		c.saveSettings()
	}
	if c.statusLabel != nil && strings.TrimSpace(c.statusLabel.Text()) == "" {
		c.setStatus("변경 사항은 자동 저장됩니다. 오버레이를 직접 움직이거나 크기를 바꾸면 값이 바로 반영됩니다.")
	}
}

func (c *controller) syncControlsFromActive() {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	c.syncingControls = true
	if c.zoomXEdit != nil {
		c.zoomXEdit.SetText(formatZoom(entry.profile.ZoomX))
	}
	if c.zoomYEdit != nil {
		c.zoomYEdit.SetText(formatZoom(entry.profile.ZoomY))
	}
	if c.opacitySlider != nil {
		c.opacitySlider.SetValue(entry.profile.Opacity)
	}
	if c.refreshSlider != nil {
		c.refreshSlider.SetValue(entry.profile.RefreshRate)
		c.refreshSlider.SetEnabled(entry.profile.SourceKind == model.SourceScreen)
		c.refreshSlider.SetVisible(entry.profile.SourceKind == model.SourceScreen)
	}
	if c.captureBackendPicker != nil {
		c.captureBackendPicker.SetCurrentIndex(captureBackendIndex(entry.profile.CaptureBackend))
		c.captureBackendPicker.SetEnabled(entry.profile.SourceKind == model.SourceScreen)
		c.captureBackendPicker.SetVisible(entry.profile.SourceKind == model.SourceScreen)
	}
	if c.refreshLabel != nil {
		c.refreshLabel.SetEnabled(entry.profile.SourceKind == model.SourceScreen)
		c.refreshLabel.SetVisible(entry.profile.SourceKind == model.SourceScreen)
	}
	if c.clickThroughBox != nil {
		c.clickThroughBox.SetChecked(c.globalClickThrough)
	}
	if c.alwaysOnTopBox != nil {
		c.alwaysOnTopBox.SetChecked(c.alwaysOnTop)
	}
	if c.minimizeToTrayBox != nil {
		c.minimizeToTrayBox.SetChecked(c.minimizeToTray)
	}
	if c.aspectLockBox != nil {
		c.aspectLockBox.SetChecked(entry.profile.LockAspect)
		c.aspectLockBox.SetEnabled(entry.profile.SourceKind != model.SourceText)
		c.aspectLockBox.SetVisible(entry.profile.SourceKind != model.SourceText)
	}
	if c.recursiveBlockBox != nil {
		c.recursiveBlockBox.SetChecked(entry.profile.BlockRecursiveCapture)
		c.recursiveBlockBox.SetEnabled(c.recursiveCaptureSupported(entry))
	}
	if c.textContentEdit != nil {
		c.textContentEdit.SetText(entry.profile.TextContent)
		c.textContentEdit.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textFontSizeEdit != nil {
		c.textFontSizeEdit.SetText(fmt.Sprintf("%d", entry.profile.TextFontSize))
		c.textFontSizeEdit.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textFontFamilyPicker != nil {
		c.textFontFamilyPicker.SetCurrentIndex(indexOfTextFont(entry.profile.TextFontFamily))
		c.textFontFamilyPicker.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textAlignXPicker != nil {
		c.textAlignXPicker.SetCurrentIndex(indexOfTextAlignX(entry.profile.TextAlignX))
		c.textAlignXPicker.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textAlignYPicker != nil {
		c.textAlignYPicker.SetCurrentIndex(indexOfTextAlignY(entry.profile.TextAlignY))
		c.textAlignYPicker.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textColorEdit != nil {
		c.textColorEdit.SetText(entry.profile.TextColor)
		c.textColorEdit.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textBackgroundEdit != nil {
		c.textBackgroundEdit.SetText(entry.profile.TextBackground)
		c.textBackgroundEdit.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textBackgroundAlphaSlider != nil {
		c.textBackgroundAlphaSlider.SetValue(entry.profile.TextBackgroundAlpha)
		c.textBackgroundAlphaSlider.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textBackgroundAlphaLabel != nil {
		c.textBackgroundAlphaLabel.SetText(fmt.Sprintf("%d%%", entry.profile.TextBackgroundAlpha))
		c.textBackgroundAlphaLabel.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textShadowColorEdit != nil {
		c.textShadowColorEdit.SetText(entry.profile.TextShadowColor)
		c.textShadowColorEdit.SetEnabled(entry.profile.SourceKind == model.SourceText && entry.profile.TextShadow)
	}
	if c.textShadowOffsetEdit != nil {
		c.textShadowOffsetEdit.SetText(fmt.Sprintf("%d", entry.profile.TextShadowOffset))
		c.textShadowOffsetEdit.SetEnabled(entry.profile.SourceKind == model.SourceText && entry.profile.TextShadow)
	}
	if c.textBoldBox != nil {
		c.textBoldBox.SetChecked(entry.profile.TextBold)
		c.textBoldBox.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textItalicBox != nil {
		c.textItalicBox.SetChecked(entry.profile.TextItalic)
		c.textItalicBox.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	if c.textShadowBox != nil {
		c.textShadowBox.SetChecked(entry.profile.TextShadow)
		c.textShadowBox.SetEnabled(entry.profile.SourceKind == model.SourceText)
	}
	textZoomEnabled := entry.profile.SourceKind != model.SourceText
	if c.zoomXEdit != nil {
		c.zoomXEdit.SetEnabled(textZoomEnabled)
		c.zoomXEdit.SetVisible(textZoomEnabled)
	}
	if c.zoomYEdit != nil {
		c.zoomYEdit.SetEnabled(textZoomEnabled)
		c.zoomYEdit.SetVisible(textZoomEnabled)
	}
	if c.zoomXTitleLabel != nil {
		c.zoomXTitleLabel.SetVisible(textZoomEnabled)
	}
	if c.zoomXCurrentLabel != nil {
		c.zoomXCurrentLabel.SetVisible(textZoomEnabled)
	}
	if c.zoomXLabel != nil {
		c.zoomXLabel.SetVisible(textZoomEnabled)
	}
	if c.zoomYTitleLabel != nil {
		c.zoomYTitleLabel.SetVisible(textZoomEnabled)
	}
	if c.zoomYCurrentLabel != nil {
		c.zoomYCurrentLabel.SetVisible(textZoomEnabled)
	}
	if c.zoomYLabel != nil {
		c.zoomYLabel.SetVisible(textZoomEnabled)
	}
	if c.refreshTitleLabel != nil {
		c.refreshTitleLabel.SetVisible(entry.profile.SourceKind == model.SourceScreen)
	}
	if c.refreshCurrentTitleLabel != nil {
		c.refreshCurrentTitleLabel.SetVisible(entry.profile.SourceKind == model.SourceScreen)
	}
	if c.screenSourceBox != nil {
		c.screenSourceBox.SetVisible(entry.profile.SourceKind == model.SourceScreen)
	}
	if c.imageSourceBox != nil {
		c.imageSourceBox.SetVisible(entry.profile.SourceKind == model.SourceImage)
	}
	if c.textSourceBox != nil {
		c.textSourceBox.SetVisible(entry.profile.SourceKind == model.SourceText)
	}
	if c.sourceKindPicker != nil {
		c.sourceKindPicker.SetCurrentIndex(sourceKindIndex(entry.profile.SourceKind))
	}
	c.syncingControls = false
	c.refreshOverlayPicker()
	c.enforceMainWindowFixedSize()
}

func (c *controller) enforceMainWindowFixedSize() {
	if c.mainWindow == nil || win.IsIconic(c.mainWindow.Handle()) {
		return
	}
	bounds := c.mainWindow.Bounds()
	if bounds.Width == mainWindowFixedWidth && bounds.Height == mainWindowFixedHeight {
		return
	}
	_ = c.mainWindow.SetBounds(walk.Rectangle{X: bounds.X, Y: bounds.Y, Width: mainWindowFixedWidth, Height: mainWindowFixedHeight})
	c.captureMainWindowRect()
}

func (c *controller) updateLabels() {
	entry := c.activeEntry()
	if entry == nil {
		return
	}
	if c.sourceLabel != nil {
		if entry.profile.SourceKind == model.SourceImage {
			c.sourceLabel.SetText("소스: 이미지 파일 · " + entry.profile.ImagePath)
		} else if entry.profile.SourceKind == model.SourceText {
			c.sourceLabel.SetText("소스: 텍스트 · " + entry.profile.TextFontFamily)
		} else {
			actualBackend := captureBackendLabel(entry.actualCaptureBackend)
			if strings.TrimSpace(entry.actualCaptureBackend) == "" {
				actualBackend = "확인 중"
			}
			c.sourceLabel.SetText("소스: 화면 캡처 · 설정 백엔드: " + captureBackendLabel(entry.profile.CaptureBackend) + " · 실제 백엔드: " + actualBackend)
		}
	}
	if c.captureLabel != nil {
		if entry.profile.SourceKind == model.SourceImage {
			c.captureLabel.SetText(fmt.Sprintf("이미지 크기: %d x %d", entry.profile.CaptureRect.Width, entry.profile.CaptureRect.Height))
		} else if entry.profile.SourceKind == model.SourceText {
			c.captureLabel.SetText(fmt.Sprintf("텍스트 캔버스 크기: %d x %d · %s/%s 정렬", entry.profile.CaptureRect.Width, entry.profile.CaptureRect.Height, textAlignXLabel(entry.profile.TextAlignX), textAlignYLabel(entry.profile.TextAlignY)))
		} else {
			c.captureLabel.SetText(formatRect("캡처 영역", entry.profile.CaptureRect))
		}
	}
	if c.overlayLabel != nil {
		c.overlayLabel.SetText(formatRect("오버레이 영역", entry.profile.OverlayRect))
	}
	if c.opacityLabel != nil {
		c.opacityLabel.SetText(fmt.Sprintf("%d%%", entry.profile.Opacity))
	}
	if c.zoomXLabel != nil {
		c.zoomXLabel.SetText(formatZoom(entry.profile.ZoomX) + "%")
	}
	if c.zoomYLabel != nil {
		c.zoomYLabel.SetText(formatZoom(entry.profile.ZoomY) + "%")
	}
	if c.refreshLabel != nil {
		c.refreshLabel.SetText(fmt.Sprintf("%d Hz", entry.profile.RefreshRate))
	}
}

func captureBackendIndex(backend string) int {
	switch backend {
	case model.CaptureBackendGDI:
		return 1
	case model.CaptureBackendDDA:
		return 2
	default:
		return 0
	}
}

func captureBackendValue(index int) string {
	switch index {
	case 1:
		return model.CaptureBackendGDI
	case 2:
		return model.CaptureBackendDDA
	default:
		return model.CaptureBackendAuto
	}
}

func captureBackendLabel(backend string) string {
	switch backend {
	case model.CaptureBackendGDI:
		return "기존 GDI BitBlt"
	case model.CaptureBackendDDA:
		return "Desktop Duplication (DXGI)"
	default:
		return "자동"
	}
}

func (c *controller) recursiveCaptureSupported(entry *overlayEntry) bool {
	if entry == nil || entry.profile.SourceKind != model.SourceScreen {
		return false
	}
	return entry.actualCaptureBackend == model.CaptureBackendDDA
}

func (c *controller) updateRecursiveCaptureState(entry *overlayEntry) {
	if entry == nil || entry.overlay == nil {
		return
	}
	excluded := entry.profile.BlockRecursiveCapture && c.recursiveCaptureSupported(entry)
	if err := entry.overlay.SetCaptureExcluded(excluded); err != nil && c.mainWindow != nil {
		c.mainWindow.Synchronize(func() {
			c.setStatus("재귀 캡처 차단 적용 실패: " + err.Error())
		})
	}
}

func (c *controller) setStatus(message string) {
	appendRuntimeLog(runtimeLogLevelForStatus(message), message)
	if c.statusLabel != nil {
		if strings.TrimSpace(message) == "" {
			message = "준비됨"
		}
		c.statusLabel.SetText(message)
	}
}

func (c *controller) settingsPathText() string {
	if c.store == nil || strings.TrimSpace(c.store.SettingsPath()) == "" {
		return "자동 저장 경로: 확인 불가"
	}
	return "자동 저장 경로: " + c.store.SettingsPath()
}

func (c *controller) overlayBoundsChanged(id string, rect model.Rect, final bool) {
	if rect.Empty() {
		return
	}
	for index, entry := range c.overlays {
		if entry.profile.ID != id {
			continue
		}
		entry.profile.OverlayRect = rect
		if entry.profile.SourceKind == model.SourceText {
			syncTextCaptureRect(&entry.profile)
			if entry.capture != nil {
				entry.capture.UpdateProfile(entry.profile)
			}
		}
		entry.profile = entry.profile.Sanitized()
		c.syncZoomFromOverlay(entry)
		if index == c.activeIndex {
			c.syncControlsFromActive()
			c.updateLabels()
		}
		if final {
			c.saveSettings()
		}
		return
	}
}

func (c *controller) applyZoomToOverlay(entry *overlayEntry, changedAxis string) {
	if entry == nil || entry.profile.CaptureRect.Empty() {
		return
	}
	if entry.profile.SourceKind == model.SourceText {
		entry.profile.ZoomX = 100
		entry.profile.ZoomY = 100
		return
	}
	rect := entry.profile.OverlayRect
	if rect.Empty() {
		rect = model.DefaultProfile(virtualScreenBounds()).OverlayRect
	}
	if entry.profile.LockAspect {
		ratio := entry.profile.EffectiveAspectRatio()
		if ratio <= 0 {
			ratio = 1
		}
		switch changedAxis {
		case "y":
			rect.Height = overlayPixelsForZoom(entry.profile.CaptureRect.Height, entry.profile.ZoomY)
			rect.Width = maxOverlaySize(int(float64(rect.Height) * ratio))
		default:
			rect.Width = overlayPixelsForZoom(entry.profile.CaptureRect.Width, entry.profile.ZoomX)
			rect.Height = maxOverlaySize(int(float64(rect.Width) / ratio))
		}
	} else {
		rect.Width = overlayPixelsForZoom(entry.profile.CaptureRect.Width, entry.profile.ZoomX)
		rect.Height = overlayPixelsForZoom(entry.profile.CaptureRect.Height, entry.profile.ZoomY)
	}
	entry.profile.OverlayRect = rect
	c.syncZoomFromOverlay(entry)
}

func (c *controller) syncZoomFromOverlay(entry *overlayEntry) {
	if entry == nil || entry.profile.CaptureRect.Empty() || entry.profile.OverlayRect.Empty() {
		return
	}
	if entry.profile.SourceKind == model.SourceText {
		entry.profile.ZoomX = 100
		entry.profile.ZoomY = 100
		return
	}
	entry.profile.ZoomX = zoomPercentForSizes(entry.profile.OverlayRect.Width, entry.profile.CaptureRect.Width)
	entry.profile.ZoomY = zoomPercentForSizes(entry.profile.OverlayRect.Height, entry.profile.CaptureRect.Height)
	if entry.profile.LockAspect {
		entry.profile.ZoomY = entry.profile.ZoomX
	}
}

func (c *controller) currentSession() model.Session {
	profiles := make([]model.Profile, 0, len(c.overlays))
	activeID := ""
	c.captureMainWindowRect()
	for index, entry := range c.overlays {
		profile := entry.profile
		profile.ClickThrough = c.globalClickThrough
		profiles = append(profiles, profile.Sanitized())
		if index == c.activeIndex {
			activeID = entry.profile.ID
		}
	}
	return model.Session{
		ActiveOverlayID: activeID,
		MainWindowRect:  c.mainWindowRect,
		GlobalClickThrough: c.globalClickThrough,
		MinimizeToTray: c.minimizeToTray,
		AlwaysOnTop:    c.alwaysOnTop,
		Overlays:        profiles,
	}
}

func sessionGlobalClickThrough(session model.Session) bool {
	if session.GlobalClickThrough || len(session.Overlays) == 0 {
		return session.GlobalClickThrough || model.DefaultProfile(virtualScreenBounds()).ClickThrough
	}
	return session.Overlays[0].ClickThrough
}

func sessionMainWindowRect(session model.Session, fallback model.Rect) model.Rect {
	if !session.MainWindowRect.Empty() {
		return session.MainWindowRect
	}
	return fallback
}

func (c *controller) initializeShellIntegration() error {
	icon, err := loadShellAppIcon()
	if err != nil {
		return err
	}
	c.appIcon = icon
	if c.mainWindow != nil {
		if err := c.mainWindow.SetIcon(icon); err != nil {
			return err
		}
	}
	tray, err := walk.NewNotifyIcon(c.mainWindow)
	if err != nil {
		return err
	}
	c.trayIcon = tray
	if err := c.trayIcon.SetIcon(icon); err != nil {
		return err
	}
	if err := c.trayIcon.SetToolTip(appName); err != nil {
		return err
	}
	c.trayIcon.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		now := time.Now()
		if !c.lastTrayLeftClick.IsZero() && now.Sub(c.lastTrayLeftClick) <= 500*time.Millisecond {
			c.lastTrayLeftClick = time.Time{}
			c.restoreMainWindowFromTray()
			return
		}
		c.lastTrayLeftClick = now
	})
	openAction := walk.NewAction()
	_ = openAction.SetText("설정창 열기")
	openAction.Triggered().Attach(func() {
		c.restoreMainWindowFromTray()
	})
	if err := c.trayIcon.ContextMenu().Actions().Add(openAction); err != nil {
		return err
	}
	exitAction := walk.NewAction()
	_ = exitAction.SetText("종료")
	exitAction.Triggered().Attach(func() {
		c.requestExit()
	})
	if err := c.trayIcon.ContextMenu().Actions().Add(exitAction); err != nil {
		return err
	}
	return c.trayIcon.SetVisible(false)
}

func (c *controller) disposeTrayIcon() {
	if c.trayIcon != nil {
		_ = c.trayIcon.Dispose()
		c.trayIcon = nil
	}
}

func (c *controller) applyMainWindowOptions() {
	if c.mainWindow != nil {
		winutil.SetAlwaysOnTop(c.mainWindow.Handle(), c.alwaysOnTop)
	}
	if c.trayIcon != nil {
		_ = c.trayIcon.SetVisible(c.minimizeToTray)
	}
}

func (c *controller) minimizeMainWindowToTray() {
	if c.mainWindow == nil || c.exitRequested || !c.minimizeToTray {
		return
	}
	c.captureMainWindowRect()
	if c.trayIcon != nil {
		_ = c.trayIcon.SetVisible(true)
	}
	c.mainWindow.Hide()
	win.ShowWindow(c.mainWindow.Handle(), win.SW_HIDE)
	c.saveSettings()
	if c.statusLabel != nil {
		c.setStatus("설정창이 트레이로 최소화되었습니다.")
	}
}

func (c *controller) restoreMainWindowFromTray() {
	if c.mainWindow == nil {
		return
	}
	win.ShowWindow(c.mainWindow.Handle(), win.SW_RESTORE)
	win.ShowWindow(c.mainWindow.Handle(), win.SW_SHOW)
	c.mainWindow.Show()
	c.mainWindow.SetVisible(true)
	win.SetForegroundWindow(c.mainWindow.Handle())
	c.applyMainWindowOptions()
	if c.minimizeToTray && c.trayIcon != nil {
		_ = c.trayIcon.SetVisible(true)
	}
	if c.statusLabel != nil {
		c.setStatus("설정창을 복원했습니다.")
	}
}

func (c *controller) captureMainWindowRect() {
	if c.mainWindow == nil || win.IsIconic(c.mainWindow.Handle()) {
		return
	}
	bounds := c.mainWindow.Bounds()
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return
	}
	c.mainWindowRect = model.Rect{X: bounds.X, Y: bounds.Y, Width: bounds.Width, Height: bounds.Height}
}

func (c *controller) requestExit() {
	c.exitRequested = true
	appendRuntimeLog("INFO", "application shutdown requested")
	c.captureMainWindowRect()
	c.saveSettings()
	c.disposeTrayIcon()
	c.disposeOverlays()
	if c.mainWindow != nil {
		winutil.SetAlwaysOnTop(c.mainWindow.Handle(), false)
		c.mainWindow.SetVisible(false)
		c.mainWindow.Close()
	}
}

func (c *controller) saveSettings() {
	if c.store == nil {
		return
	}
	if err := c.store.SaveSettings(c.currentSession()); err != nil {
		c.setStatus("자동 저장 실패: " + err.Error())
	}
}

func (c *controller) newOverlayID() string {
	c.nextOverlaySeed++
	return fmt.Sprintf("overlay-%d-%d", time.Now().UnixNano(), c.nextOverlaySeed)
}

func syncImageCaptureRect(profile *model.Profile) error {
	if profile == nil || profile.ImagePath == "" {
		return nil
	}
	file, err := os.Open(profile.ImagePath)
	if err != nil {
		return err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return err
	}
	profile.CaptureRect.Width = config.Width
	profile.CaptureRect.Height = config.Height
	return nil
}

func syncTextCaptureRect(profile *model.Profile) {
	if profile == nil || profile.SourceKind != model.SourceText {
		return
	}
	width := profile.OverlayRect.Width
	height := profile.OverlayRect.Height
	if width < 64 {
		width = 640
	}
	if height < 64 {
		height = 240
	}
	profile.CaptureRect.Width = width
	profile.CaptureRect.Height = height
}

func sourceKindIndex(kind string) int {
	switch kind {
	case model.SourceImage:
		return 1
	case model.SourceText:
		return 2
	default:
		return 0
	}
}

func alignXFromIndex(index int) string {
	switch index {
	case 2:
		return model.TextAlignRight
	case 1:
		return model.TextAlignCenter
	default:
		return model.TextAlignLeft
	}
}

func alignYFromIndex(index int) string {
	switch index {
	case 2:
		return model.TextAlignBottom
	case 1:
		return model.TextAlignMiddle
	default:
		return model.TextAlignTop
	}
}

func indexOfTextAlignX(value string) int {
	switch value {
	case model.TextAlignRight:
		return 2
	case model.TextAlignCenter:
		return 1
	default:
		return 0
	}
}

func indexOfTextAlignY(value string) int {
	switch value {
	case model.TextAlignBottom:
		return 2
	case model.TextAlignMiddle:
		return 1
	default:
		return 0
	}
}

func indexOfTextFont(family string) int {
	for index, option := range textFontOptions {
		if strings.EqualFold(option, family) {
			return index
		}
	}
	return 0
}

func textAlignXLabel(value string) string {
	switch value {
	case model.TextAlignRight:
		return "오른쪽"
	case model.TextAlignCenter:
		return "가운데"
	default:
		return "왼쪽"
	}
}

func textAlignYLabel(value string) string {
	switch value {
	case model.TextAlignBottom:
		return "아래"
	case model.TextAlignMiddle:
		return "가운데"
	default:
		return "위"
	}
}

func parseZoomInput(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0, err
	}
	if parsed < model.MinZoom {
		parsed = model.MinZoom
	}
	if parsed > model.MaxZoom {
		parsed = model.MaxZoom
	}
	return parsed, nil
}

func formatZoom(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

func zoomPercentForSizes(overlaySize, captureSize int) float64 {
	if overlaySize <= 0 || captureSize <= 0 {
		return 100
	}
	value := (float64(overlaySize) * 100) / float64(captureSize)
	if value < model.MinZoom {
		return model.MinZoom
	}
	if value > model.MaxZoom {
		return model.MaxZoom
	}
	return value
}

func overlayPixelsForZoom(captureSize int, zoom float64) int {
	if captureSize <= 0 {
		return model.MinOverlaySize
	}
	if zoom < model.MinZoom {
		zoom = model.MinZoom
	}
	if zoom > model.MaxZoom {
		zoom = model.MaxZoom
	}
	return maxOverlaySize(int((float64(captureSize) * zoom) / 100.0))
}

func maxOverlaySize(value int) int {
	if value < model.MinOverlaySize {
		return model.MinOverlaySize
	}
	return value
}

func formatRect(label string, rect model.Rect) string {
	return fmt.Sprintf("%s: X=%d, Y=%d, W=%d, H=%d", label, rect.X, rect.Y, rect.Width, rect.Height)
}
