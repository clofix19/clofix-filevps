package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type applicationCron struct {
	fields                   [5]map[int]bool
	domWildcard, dowWildcard bool
}

func parseApplicationCron(spec string) (applicationCron, error) {
	c := applicationCron{}
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return c, fmt.Errorf("schedule requires five UTC cron fields: minute hour day month weekday")
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, field := range parts {
		c.fields[i] = map[int]bool{}
		for _, piece := range strings.Split(field, ",") {
			rangePart, stepPart, hasStep := strings.Cut(piece, "/")
			step := 1
			if hasStep {
				var err error
				step, err = strconv.Atoi(stepPart)
				if err != nil || step < 1 || step > 60 {
					return c, fmt.Errorf("invalid cron step")
				}
			}
			lo, hi := bounds[i][0], bounds[i][1]
			if rangePart != "*" {
				a, b, hasRange := strings.Cut(rangePart, "-")
				var err error
				lo, err = strconv.Atoi(a)
				if err != nil {
					return c, fmt.Errorf("invalid cron value")
				}
				hi = lo
				if hasRange {
					hi, err = strconv.Atoi(b)
					if err != nil {
						return c, fmt.Errorf("invalid cron range")
					}
				} else if hasStep {
					hi = bounds[i][1]
				}
			}
			if lo < bounds[i][0] || hi > bounds[i][1] || lo > hi {
				return c, fmt.Errorf("cron field outside allowed range")
			}
			for n := lo; n <= hi; n += step {
				c.fields[i][n] = true
			}
		}
	}
	c.domWildcard = strings.HasPrefix(parts[2], "*")
	c.dowWildcard = strings.HasPrefix(parts[4], "*")
	return c, nil
}
func (c applicationCron) matches(t time.Time) bool {
	t = t.UTC()
	if !c.fields[0][t.Minute()] || !c.fields[1][t.Hour()] || !c.fields[3][int(t.Month())] {
		return false
	}
	dom, dow := c.fields[2][t.Day()], c.fields[4][int(t.Weekday())]
	if !c.domWildcard && !c.dowWildcard {
		return dom || dow
	}
	return dom && dow
}
func (s *Server) RunApplicationScheduler(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		s.runApplicationSchedules(time.Now().UTC())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Server) runApplicationSchedules(now time.Time) {
	d := s.applications
	d.Lock()
	apps := []application{}
	minute := now.UTC().Format("2006-01-02T15:04")
	for id, a := range d.Apps {
		if a.Schedule == "" || a.Kind != "job" || a.LastSchedule == minute {
			continue
		}
		cron, err := parseApplicationCron(a.Schedule)
		if err != nil || !cron.matches(now) {
			continue
		}
		old := a
		a.LastSchedule = minute
		d.Apps[id] = a
		if d.save() != nil {
			d.Apps[id] = old
			continue
		}
		apps = append(apps, a)
	}
	d.Unlock()
	for _, a := range apps {
		p, ok := s.applicationTriggerPrincipal(a.Owner)
		var err error
		if !ok {
			err = fmt.Errorf("application owner cannot execute scheduled jobs")
		} else {
			req, _ := http.NewRequest("POST", "/api/applications/"+a.ID+"/run", strings.NewReader(`{"input":""}`))
			req.SetPathValue("id", a.ID)
			req = req.WithContext(context.WithValue(context.Background(), iamPrincipalContextKey{}, p))
			_, err = s.queueApplication(req, "job")
		}
		d.Lock()
		current, exists := d.Apps[a.ID]
		if exists {
			current.ScheduleError = ""
			if err != nil {
				current.ScheduleError = err.Error()
			}
			d.Apps[a.ID] = current
			_ = d.save()
		}
		d.Unlock()
	}
}
