package translation

import "github.com/golaravel/golaravel/pkg/context"

func init() {
	context.SetTranslator(defaultTranslator)
}
