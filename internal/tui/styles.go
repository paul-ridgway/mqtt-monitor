package tui

import "github.com/charmbracelet/lipgloss"

var (
	colAccent  = lipgloss.AdaptiveColor{Light: "#5A3FC0", Dark: "#A78BFA"}
	colDim     = lipgloss.AdaptiveColor{Light: "#8A8A8A", Dark: "#6B6B6B"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#555555", Dark: "#A0A0A0"}
	colText    = lipgloss.AdaptiveColor{Light: "#1A1A1A", Dark: "#E6E6E6"}
	colTopic   = lipgloss.AdaptiveColor{Light: "#0B6E99", Dark: "#7DD3FC"}
	colGood    = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#4ADE80"}
	colWarn    = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#FACC15"}
	colBad     = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F87171"}
	colFresh   = lipgloss.AdaptiveColor{Light: "#BF3989", Dark: "#F472B6"}
	colSelBg   = lipgloss.AdaptiveColor{Light: "#DDD6FE", Dark: "#3B2F66"}
	colBarBg   = lipgloss.AdaptiveColor{Light: "#E4E4E7", Dark: "#27272A"}
	colJSONKey = lipgloss.AdaptiveColor{Light: "#0B6E99", Dark: "#7DD3FC"}
	colJSONStr = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#86EFAC"}
	colJSONNum = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#FCD34D"}
	colJSONLit = lipgloss.AdaptiveColor{Light: "#BF3989", Dark: "#F9A8D4"}
)

var (
	stDim      = lipgloss.NewStyle().Foreground(colDim)
	stMuted    = lipgloss.NewStyle().Foreground(colMuted)
	stText     = lipgloss.NewStyle().Foreground(colText)
	stTopic    = lipgloss.NewStyle().Foreground(colTopic)
	stFresh    = lipgloss.NewStyle().Foreground(colFresh)
	stAccent   = lipgloss.NewStyle().Foreground(colAccent)
	stBold     = lipgloss.NewStyle().Bold(true)
	stGood     = lipgloss.NewStyle().Foreground(colGood)
	stWarn     = lipgloss.NewStyle().Foreground(colWarn)
	stBad      = lipgloss.NewStyle().Foreground(colBad)
	stSelected = lipgloss.NewStyle().Background(colSelBg)
	stKey      = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stJSONKey  = lipgloss.NewStyle().Foreground(colJSONKey)
	stJSONStr  = lipgloss.NewStyle().Foreground(colJSONStr)
	stJSONNum  = lipgloss.NewStyle().Foreground(colJSONNum)
	stJSONLit  = lipgloss.NewStyle().Foreground(colJSONLit)
	stBar      = lipgloss.NewStyle().Background(colBarBg).Foreground(colText)
)
