package evidence

import "time"

type Evidence struct {
	Source     string
	Kind       string
	At         time.Time
	Confidence float64
	Summary    string
	Data       map[string]any
	Links      []Link
}

type Link struct {
	Label string
	URL   string
}
