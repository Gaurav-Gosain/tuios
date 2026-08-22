package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// KeyMod represents a key modifier.
type KeyMod = uv.KeyMod

// Modifier keys.
const (
	ModShift = uv.ModShift
	ModAlt   = uv.ModAlt
	ModCtrl  = uv.ModCtrl
	ModMeta  = uv.ModMeta
)

// KeyPressEvent represents a key press event.
type KeyPressEvent = uv.KeyPressEvent

// Key codes.
const (
	KeyExtended         = uv.KeyExtended
	KeyUp               = uv.KeyUp
	KeyDown             = uv.KeyDown
	KeyRight            = uv.KeyRight
	KeyLeft             = uv.KeyLeft
	KeyBegin            = uv.KeyBegin
	KeyFind             = uv.KeyFind
	KeyInsert           = uv.KeyInsert
	KeyDelete           = uv.KeyDelete
	KeySelect           = uv.KeySelect
	KeyPgUp             = uv.KeyPgUp
	KeyPgDown           = uv.KeyPgDown
	KeyHome             = uv.KeyHome
	KeyEnd              = uv.KeyEnd
	KeyKpEnter          = uv.KeyKpEnter
	KeyKpEqual          = uv.KeyKpEqual
	KeyKpMultiply       = uv.KeyKpMultiply
	KeyKpPlus           = uv.KeyKpPlus
	KeyKpComma          = uv.KeyKpComma
	KeyKpMinus          = uv.KeyKpMinus
	KeyKpDecimal        = uv.KeyKpDecimal
	KeyKpDivide         = uv.KeyKpDivide
	KeyKp0              = uv.KeyKp0
	KeyKp1              = uv.KeyKp1
	KeyKp2              = uv.KeyKp2
	KeyKp3              = uv.KeyKp3
	KeyKp4              = uv.KeyKp4
	KeyKp5              = uv.KeyKp5
	KeyKp6              = uv.KeyKp6
	KeyKp7              = uv.KeyKp7
	KeyKp8              = uv.KeyKp8
	KeyKp9              = uv.KeyKp9
	KeyKpSep            = uv.KeyKpSep
	KeyKpUp             = uv.KeyKpUp
	KeyKpDown           = uv.KeyKpDown
	KeyKpLeft           = uv.KeyKpLeft
	KeyKpRight          = uv.KeyKpRight
	KeyKpPgUp           = uv.KeyKpPgUp
	KeyKpPgDown         = uv.KeyKpPgDown
	KeyKpHome           = uv.KeyKpHome
	KeyKpEnd            = uv.KeyKpEnd
	KeyKpInsert         = uv.KeyKpInsert
	KeyKpDelete         = uv.KeyKpDelete
	KeyKpBegin          = uv.KeyKpBegin
	KeyF1               = uv.KeyF1
	KeyF2               = uv.KeyF2
	KeyF3               = uv.KeyF3
	KeyF4               = uv.KeyF4
	KeyF5               = uv.KeyF5
	KeyF6               = uv.KeyF6
	KeyF7               = uv.KeyF7
	KeyF8               = uv.KeyF8
	KeyF9               = uv.KeyF9
	KeyF10              = uv.KeyF10
	KeyF11              = uv.KeyF11
	KeyF12              = uv.KeyF12
	KeyF13              = uv.KeyF13
	KeyF14              = uv.KeyF14
	KeyF15              = uv.KeyF15
	KeyF16              = uv.KeyF16
	KeyF17              = uv.KeyF17
	KeyF18              = uv.KeyF18
	KeyF19              = uv.KeyF19
	KeyF20              = uv.KeyF20
	KeyF21              = uv.KeyF21
	KeyF22              = uv.KeyF22
	KeyF23              = uv.KeyF23
	KeyF24              = uv.KeyF24
	KeyF25              = uv.KeyF25
	KeyF26              = uv.KeyF26
	KeyF27              = uv.KeyF27
	KeyF28              = uv.KeyF28
	KeyF29              = uv.KeyF29
	KeyF30              = uv.KeyF30
	KeyF31              = uv.KeyF31
	KeyF32              = uv.KeyF32
	KeyF33              = uv.KeyF33
	KeyF34              = uv.KeyF34
	KeyF35              = uv.KeyF35
	KeyF36              = uv.KeyF36
	KeyF37              = uv.KeyF37
	KeyF38              = uv.KeyF38
	KeyF39              = uv.KeyF39
	KeyF40              = uv.KeyF40
	KeyF41              = uv.KeyF41
	KeyF42              = uv.KeyF42
	KeyF43              = uv.KeyF43
	KeyF44              = uv.KeyF44
	KeyF45              = uv.KeyF45
	KeyF46              = uv.KeyF46
	KeyF47              = uv.KeyF47
	KeyF48              = uv.KeyF48
	KeyF49              = uv.KeyF49
	KeyF50              = uv.KeyF50
	KeyF51              = uv.KeyF51
	KeyF52              = uv.KeyF52
	KeyF53              = uv.KeyF53
	KeyF54              = uv.KeyF54
	KeyF55              = uv.KeyF55
	KeyF56              = uv.KeyF56
	KeyF57              = uv.KeyF57
	KeyF58              = uv.KeyF58
	KeyF59              = uv.KeyF59
	KeyF60              = uv.KeyF60
	KeyF61              = uv.KeyF61
	KeyF62              = uv.KeyF62
	KeyF63              = uv.KeyF63
	KeyCapsLock         = uv.KeyCapsLock
	KeyScrollLock       = uv.KeyScrollLock
	KeyNumLock          = uv.KeyNumLock
	KeyPrintScreen      = uv.KeyPrintScreen
	KeyPause            = uv.KeyPause
	KeyMenu             = uv.KeyMenu
	KeyMediaPlay        = uv.KeyMediaPlay
	KeyMediaPause       = uv.KeyMediaPause
	KeyMediaPlayPause   = uv.KeyMediaPlayPause
	KeyMediaReverse     = uv.KeyMediaReverse
	KeyMediaStop        = uv.KeyMediaStop
	KeyMediaFastForward = uv.KeyMediaFastForward
	KeyMediaRewind      = uv.KeyMediaRewind
	KeyMediaNext        = uv.KeyMediaNext
	KeyMediaPrev        = uv.KeyMediaPrev
	KeyMediaRecord      = uv.KeyMediaRecord
	KeyLowerVol         = uv.KeyLowerVol
	KeyRaiseVol         = uv.KeyRaiseVol
	KeyMute             = uv.KeyMute
	KeyLeftShift        = uv.KeyLeftShift
	KeyLeftAlt          = uv.KeyLeftAlt
	KeyLeftCtrl         = uv.KeyLeftCtrl
	KeyLeftSuper        = uv.KeyLeftSuper
	KeyLeftHyper        = uv.KeyLeftHyper
	KeyLeftMeta         = uv.KeyLeftMeta
	KeyRightShift       = uv.KeyRightShift
	KeyRightAlt         = uv.KeyRightAlt
	KeyRightCtrl        = uv.KeyRightCtrl
	KeyRightSuper       = uv.KeyRightSuper
	KeyRightHyper       = uv.KeyRightHyper
	KeyRightMeta        = uv.KeyRightMeta
	KeyIsoLevel3Shift   = uv.KeyIsoLevel3Shift
	KeyIsoLevel5Shift   = uv.KeyIsoLevel5Shift
	KeyBackspace        = uv.KeyBackspace
	KeyTab              = uv.KeyTab
	KeyEnter            = uv.KeyEnter
	KeyReturn           = uv.KeyReturn
	KeyEscape           = uv.KeyEscape
	KeyEsc              = uv.KeyEsc
	KeySpace            = uv.KeySpace
)
