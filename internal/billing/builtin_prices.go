package billing

import "strings"

// Builtin prices are shipped with the plugin and are intentionally not persisted.
var builtinPrices = map[string]CustomPrice{
	NormalizeModelID("codex-auto-review"): {
		ModelID:    "codex-auto-review",
		PriceRates: PriceRates{InputPer1M: 0, OutputPer1M: 0},
	},
	NormalizeModelID("gpt-image-1.5"): {
		ModelID: "gpt-image-1.5",
		PriceRates: PriceRates{
			InputPer1M:     5,
			OutputPer1M:    32,
			CacheReadPer1M: float64Ptr(1.25),
		},
	},
	NormalizeModelID("gpt-image-2.5"): {
		ModelID: "gpt-image-2.5",
		PriceRates: PriceRates{
			InputPer1M:     5,
			OutputPer1M:    30,
			CacheReadPer1M: float64Ptr(1.25),
		},
	},
	NormalizeModelID("deepseek-flash"): {
		ModelID: "deepseek-flash",
		PriceRates: PriceRates{
			InputPer1M:     0.15,
			OutputPer1M:    0.6,
			CacheReadPer1M: float64Ptr(0.003),
		},
	},
}

func float64Ptr(value float64) *float64 {
	return &value
}

func resolveBuiltinRates(modelID string) (PriceRates, bool) {
	key := NormalizeModelID(modelID)
	if slash := strings.LastIndexByte(key, '/'); slash >= 0 {
		key = key[slash+1:]
	}
	price, found := builtinPrices[key]
	return price.PriceRates, found
}

func ResolveBuiltinPrice(modelID string) Price {
	if rates, found := resolveBuiltinRates(modelID); found {
		return rates.resolve(PriceSourceBuiltin)
	}
	return Price{Source: PriceSourceNone}
}
