package controller

import (
	"text/template"
)

func GetTemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int64) int64 { return a + b },
		"sub": func(a, b int64) int64 { return a - b },
		"mul": func(a, b int64) int64 { return a * b },
		"div": func(a, b int64) int64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"mod": func(a, b int64) int64 {
			if b == 0 {
				return 0
			}
			return a % b
		},
		"addf": func(a, b float64) float64 { return a + b },
		"subf": func(a, b float64) float64 { return a - b },
		"mulf": func(a, b float64) float64 { return a * b },
		"divf": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"float64": func(a int64) float64 { return float64(a) },
	}
}
