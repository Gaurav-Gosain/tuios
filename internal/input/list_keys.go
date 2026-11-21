package input

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
)

func listKey(o *app.OS, key string, letters bool, page int, move func(delta int)) bool {
	motion := o.ListKeys.Keys(key, letters)
	if motion == listnav.None {
		return false
	}
	move(listnav.Delta(motion, page))
	return true
}

const listPage = 10
