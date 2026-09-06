package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Palette from DiveVault's frontend/app/assets/styles.css (:root dark theme).
// Keep the desktop dark palette consistent regardless of the OS theme setting.
type diveVaultTheme struct{ fyne.Theme }

func newDiveVaultTheme() fyne.Theme { return diveVaultTheme{theme.DarkTheme()} }

func (t diveVaultTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return color.NRGBA{0, 21, 37, 255} // background
	case theme.ColorNameInputBackground, theme.ColorNameScrollBarBackground:
		return color.NRGBA{2, 29, 48, 255} // surface-container-low
	case theme.ColorNameOverlayBackground:
		return color.NRGBA{6, 33, 53, 255} // surface-container (cards/dialogs)
	case theme.ColorNameButton, theme.ColorNameMenuBackground, theme.ColorNameHeaderBackground:
		return color.NRGBA{19, 44, 64, 255} // surface-container-high
	case theme.ColorNameDisabledButton:
		return color.NRGBA{31, 55, 75, 255} // surface-container-highest
	case theme.ColorNameForeground:
		return color.NRGBA{205, 229, 255, 255} // on-surface
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return color.NRGBA{156, 202, 255, 255} // primary
	case theme.ColorNameForegroundOnPrimary:
		return color.NRGBA{0, 50, 87, 255} // on-primary
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{195, 199, 205, 255} // on-surface-variant
	case theme.ColorNameDisabled:
		return color.NRGBA{141, 145, 151, 255} // outline
	case theme.ColorNameInputBorder, theme.ColorNameSeparator:
		return color.NRGBA{67, 71, 76, 255} // outline-variant
	case theme.ColorNameFocus, theme.ColorNameSelection:
		return color.NRGBA{156, 202, 255, 64}
	case theme.ColorNameHover:
		return color.NRGBA{156, 202, 255, 24}
	case theme.ColorNamePressed:
		return color.NRGBA{156, 202, 255, 48}
	case theme.ColorNameScrollBar:
		return color.NRGBA{179, 202, 214, 160} // secondary
	case theme.ColorNameWarning:
		return color.NRGBA{255, 183, 125, 255} // tertiary
	case theme.ColorNameForegroundOnWarning:
		return color.NRGBA{77, 38, 0, 255} // on-tertiary
	case theme.ColorNameError:
		return color.NRGBA{255, 180, 171, 255} // error
	case theme.ColorNameForegroundOnError:
		return color.NRGBA{147, 0, 10, 255} // error-container
	case theme.ColorNameShadow:
		return color.NRGBA{0, 15, 29, 82} // shadow-panel
	default:
		return t.Theme.Color(name, theme.VariantDark)
	}
}
