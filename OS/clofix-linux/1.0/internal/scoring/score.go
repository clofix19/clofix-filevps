package scoring

import "fmt"

// Weights documented for Security Score 0–100.
var Weights = map[string]int{
	"ssh":           15,
	"firewall":      10,
	"patch":         15,
	"vuln":          10,
	"access":        10,
	"fim":           10,
	"audit":         5,
	"confinement":   10,
	"isolation":     10,
	"backup":        5,
}

type Check struct {
	ID      string `json:"id"`
	Category string `json:"category"`
	Status  string `json:"status"` // pass|fail|unknown
	Weight  int    `json:"weight"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix,omitempty"`
}

type Result struct {
	Score      int      `json:"score"`
	Passed     []Check  `json:"passed"`
	Failed     []Check  `json:"failed"`
	Unknown    []Check  `json:"unknown"`
	Critical   []string `json:"critical_risks"`
	Fixes      []string `json:"recommended_fixes"`
	WeightsDoc string   `json:"weights_doc"`
}

func Compute(checks []Check) Result {
	r := Result{WeightsDoc: fmt.Sprintf("%v", Weights)}
	earned := 0
	possible := 0
	for _, c := range checks {
		w := c.Weight
		if w == 0 {
			w = Weights[c.Category]
		}
		possible += w
		switch c.Status {
		case "pass":
			earned += w
			r.Passed = append(r.Passed, c)
		case "fail":
			r.Failed = append(r.Failed, c)
			if c.Fix != "" {
				r.Fixes = append(r.Fixes, c.Fix)
			}
			if c.Category == "ssh" || c.Category == "access" {
				r.Critical = append(r.Critical, c.Detail)
			}
		default:
			// unknown does not count as pass
			r.Unknown = append(r.Unknown, c)
		}
	}
	if possible == 0 {
		r.Score = 0
		return r
	}
	// Scale earned/possible to 0–100; unknowns reduce denominator contribution already by not earning
	r.Score = (earned * 100) / possible
	return r
}
