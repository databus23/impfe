package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Availability struct {
	Date  string `json:"date"`
	Slots []Slot `json:"slots"`
}
type Slot struct {
	Start    string `json:"start_date"`
	End      string `json:"end_date"`
	Steps    []Step `json:"steps"`
	AgendaID int    `json:"agenda_id"`
}
type Step struct {
	Start         string `json:"start_date"`
	End           string `json:"end_date"`
	VititMotiveID int    `json:"visit_motive_id"`
	AgendaID      int    `json:"agenda_id"`
}
type AvailbilitiesResponse struct {
	Total                     int    `json:"total"`
	Reason                    string `json:"reason"`
	Message                   string `json:"message"`
	NumberOfFutureVacinations int    `json:"number_future_vaccinations"`
	NextSlot                  string `json:"next_slot"`
	Availabilities            []Availability
}

type CIZRespone struct {
	Data struct {
		Places       []Place       `json:"places"`
		Agendas      []Agenda      `json:"agendas"`
		VisitMotives []VisitMotive `json:"visit_motives"`
	} `json:"data"`
}

type Place struct {
	Name        string `json:"name"`
	PractiseIDs []int  `json:"practice_ids"`
}

type Agenda struct {
	ID                      int   `json:"id"`
	VisitMotives            []int `json:"visit_motive_ids"`
	PracticeID              int   `json:"practice_id"`
	BookingDisabled         bool  `json:"booking_disabled"`
	BookingTemporayDisabled bool  `json:"booking_temporary_disabled"`
}

type VisitMotive struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Impfzentrum struct {
	ID                  int
	Name                string
	DisabledVaccination map[int]string
	Vaccination         map[int]string
	AgendaIDs           []int
}

type ImpfzentrenCollector struct {
	impfzentrumMetric *prometheus.Desc
	nextSlotMetric    *prometheus.Desc
}

func (c *ImpfzentrenCollector) Describe(ch chan<- *prometheus.Desc) {
	if c.impfzentrumMetric == nil {
		c.impfzentrumMetric = prometheus.NewDesc("impfzentrum",
			"Zeigt Impfzentren und Art der Impfung",
			[]string{"name", "type", "disabled"}, nil,
		)
		c.nextSlotMetric = prometheus.NewDesc("impfzentrum_next_slot_duration_days",
			"Naechster verfuegbarer Termin",
			[]string{"name", "type"}, nil,
		)

	}
	ch <- c.impfzentrumMetric
}

func (cl *ImpfzentrenCollector) Collect(ch chan<- prometheus.Metric) {

	centers, err := Impfzentren()
	if err != nil {
		log.Println("Error fetching impfzentren", err)
		return
	}

	var wg sync.WaitGroup
	for _, center := range centers {
		for motiveID, motiveName := range center.Vaccination {
			wg.Add(1)
			go CollectAvailability(&wg, ch, cl.nextSlotMetric, center, motiveID, motiveName)
			ch <- prometheus.MustNewConstMetric(cl.impfzentrumMetric, prometheus.GaugeValue, 1, center.Name, motiveName, "false")
		}
		for _, v := range center.DisabledVaccination {
			ch <- prometheus.MustNewConstMetric(cl.impfzentrumMetric, prometheus.GaugeValue, 1, center.Name, v, "true")
		}

	}

	wg.Wait()

}

func main() {
	prometheus.Register(&ImpfzentrenCollector{})
	http.Handle("/metrics", promhttp.Handler())
	log.Println("Listening on :2112")
	http.ListenAndServe(":2112", nil)
}

func CollectAvailability(wg *sync.WaitGroup, ch chan<- prometheus.Metric, desc *prometheus.Desc, center Impfzentrum, motiveID int, motiveName string) {
	defer wg.Done()
	r, err := GetAvailabilities(center.ID, motiveID, center.AgendaIDs)
	if err != nil {
		log.Printf("Failed to get availabilities for %s: %s", center.Name, err)
		return
	}
	log.Printf("%#v", r)
	var nextDate string
	for _, a := range r.Availabilities {
		if len(a.Slots) > 0 {
			nextDate = a.Date
			break
		}

	}
	if nextDate == "" {
		nextDate = r.NextSlot
	}
	if nextDate != "" {
		nextSlot, err := time.Parse("2006-01-02", nextDate)
		if err != nil {
			log.Printf("Failed to get parse next slot %s: %s", nextDate, err)
			return
		}
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, time.Until(nextSlot).Hours()/24, center.Name, motiveName)
	}

}

func GetAvailabilities(practice int, motive int, aganda_ids []int) (*AvailbilitiesResponse, error) {

	u, err := url.Parse("https://www.doctolib.de/availabilities.json")
	if err != nil {
		return nil, err
	}
	aids := make([]string, 0, len(aganda_ids))
	for _, v := range aganda_ids {
		aids = append(aids, strconv.Itoa(v))
	}
	params := url.Values{}
	params.Add("start_date", time.Now().Format("2006-01-02"))
	params.Add("visit_motive_ids", strconv.Itoa(motive))
	params.Add("agenda_ids", strings.Join(aids, "-"))
	params.Add("insurance_sector", "public")
	params.Add("practice_ids", strconv.Itoa(practice))
	params.Add("destroy_temporary", "true")
	params.Add("limit", "4")

	u.RawQuery = params.Encode()
	log.Println("Calling", u)

	resp, err := http.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("Request %s failed: %w", u.String(), err)
	}
	if resp.StatusCode > 399 {
		return nil, fmt.Errorf("Request failed with: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Reading body failed: %s", err)
	}
	var availability AvailbilitiesResponse
	if err := json.Unmarshal(body, &availability); err != nil {
		return nil, fmt.Errorf("Failed to parse response %s: %w", string(body), err)
	}

	return &availability, nil

}

func Impfzentren() ([]Impfzentrum, error) {
	url := "https://www.doctolib.de/booking/ciz-berlin-berlin.json"
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("Request %s failed: %s", url, err)
	}
	if resp.StatusCode > 399 {
		return nil, fmt.Errorf("Request failed with: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Reading body failed: %s", err)
	}
	var ciz CIZRespone
	if err := json.Unmarshal(body, &ciz); err != nil {
		return nil, fmt.Errorf("Failed to parse response %s: %s", string(body), err)
	}

	motiveByID := map[int]string{}
	for _, m := range ciz.Data.VisitMotives {
		motiveByID[m.ID] = m.Name
	}
	practiceByID := map[int]*Impfzentrum{}
	for _, p := range ciz.Data.Places {
		if len(p.PractiseIDs) < 1 {
			continue
		}
		practiceByID[p.PractiseIDs[0]] = &Impfzentrum{Name: p.Name, ID: p.PractiseIDs[0], Vaccination: map[int]string{}, DisabledVaccination: map[int]string{}}
	}
	for _, a := range ciz.Data.Agendas {
		practiceByID[a.PracticeID].AgendaIDs = append(practiceByID[a.PracticeID].AgendaIDs, a.ID)
		for _, motiveID := range a.VisitMotives {

			if a.BookingDisabled || a.BookingTemporayDisabled {
				practiceByID[a.PracticeID].DisabledVaccination[motiveID] = motiveByID[motiveID]
			} else {
				practiceByID[a.PracticeID].Vaccination[motiveID] = motiveByID[motiveID]
			}
		}
	}

	result := []Impfzentrum{}
	for _, p := range practiceByID {
		result = append(result, *p)
	}

	return result, nil

}

func unique(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
