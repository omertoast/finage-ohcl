package subscriptions

type (
	OHCL struct {
		S string  `json:"s"` // symbol
		O float64 `json:"o"`
		H float64 `json:"h"`
		C float64 `json:"c"`
		L float64 `json:"l"`
		V float64 `json:"v"`
		I string  `json:"i"` // interval
	}

	OHCLChannel struct {
		Symbol   string
		Interval string
	}
)
