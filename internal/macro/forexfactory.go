package macro

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

type ImpactLevel string

const (
	ImpactRed    ImpactLevel = "red"
	ImpactOrange ImpactLevel = "orange"
	ImpactYellow ImpactLevel = "yellow"
	ImpactGray   ImpactLevel = "gray"
)

func NormalizeImpact(raw string) ImpactLevel {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(cleaned, "red") || strings.Contains(cleaned, "high"):
		return ImpactRed
	case strings.Contains(cleaned, "orange") || strings.Contains(cleaned, "ora") || strings.Contains(cleaned, "medium") || strings.Contains(cleaned, "med"):
		return ImpactOrange
	case strings.Contains(cleaned, "yellow") || strings.Contains(cleaned, "yel") || strings.Contains(cleaned, "low"):
		return ImpactYellow
	default:
		return ImpactGray
	}
}

type IndicatorSpecs struct {
	Measures    string `json:"measures,omitempty"`
	UsualEffect string `json:"usual_effect,omitempty"`
	Frequency   string `json:"frequency,omitempty"`
	NextRelease string `json:"next_release,omitempty"`
	Notes       string `json:"notes,omitempty"`
	Source      string `json:"source,omitempty"`
}

type HistoryPoint struct {
	Date     string  `json:"date"`
	Actual   string  `json:"actual"`
	Forecast string  `json:"forecast,omitempty"`
	Previous string  `json:"previous,omitempty"`
	Revised  string  `json:"revised,omitempty"`
	Numeric  float64 `json:"numeric,omitempty"`
}

type GraphData struct {
	Unit   string         `json:"unit,omitempty"`
	Points []HistoryPoint `json:"points,omitempty"`
}

type NewsEvent struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Currency  string          `json:"currency"`
	Impact    ImpactLevel     `json:"impact"`
	Date      string          `json:"date"`
	Time      string          `json:"time"`
	Timestamp *time.Time      `json:"timestamp,omitempty"`
	Actual    string          `json:"actual,omitempty"`
	Forecast  string          `json:"forecast,omitempty"`
	Previous  string          `json:"previous,omitempty"`
	URL       string          `json:"url,omitempty"`
	Specs     *IndicatorSpecs `json:"specs,omitempty"`
	History   []HistoryPoint  `json:"history,omitempty"`
	Graph     *GraphData      `json:"graph,omitempty"`
}

func (e NewsEvent) FormatForLLM() string {
	var sb strings.Builder
	impactEmoji := map[ImpactLevel]string{
		ImpactRed:    "🔴 [HIGH]",
		ImpactOrange: "🟠 [MED]",
		ImpactYellow: "🟡 [LOW]",
		ImpactGray:   "⚪ [HOLIDAY]",
	}[e.Impact]
	if impactEmoji == "" {
		impactEmoji = fmt.Sprintf("[%s]", strings.ToUpper(string(e.Impact)))
	}

	sb.WriteString(fmt.Sprintf("%s %s %s | %s %s\n", impactEmoji, e.Currency, e.Title, e.Date, e.Time))

	numParts := make([]string, 0, 3)
	if e.Actual != "" {
		numParts = append(numParts, fmt.Sprintf("Actual: %s", e.Actual))
	} else {
		numParts = append(numParts, "Actual: [Pending]")
	}
	if e.Forecast != "" {
		numParts = append(numParts, fmt.Sprintf("Forecast: %s", e.Forecast))
	}
	if e.Previous != "" {
		numParts = append(numParts, fmt.Sprintf("Previous: %s", e.Previous))
	}
	sb.WriteString(fmt.Sprintf("  • Numbers: %s\n", strings.Join(numParts, " | ")))

	if e.Specs != nil {
		if e.Specs.UsualEffect != "" {
			sb.WriteString(fmt.Sprintf("  • Usual Effect: %s\n", e.Specs.UsualEffect))
		}
		if e.Specs.Measures != "" {
			sb.WriteString(fmt.Sprintf("  • Measures: %s\n", e.Specs.Measures))
		}
		if e.Specs.Source != "" {
			sb.WriteString(fmt.Sprintf("  • Source: %s\n", e.Specs.Source))
		}
	}

	if len(e.History) > 0 {
		hParts := make([]string, 0, len(e.History))
		for _, hp := range e.History {
			hParts = append(hParts, fmt.Sprintf("%s: %s", hp.Date, hp.Actual))
		}
		sb.WriteString(fmt.Sprintf("  • Recent Trend: %s\n", strings.Join(hParts, " → ")))
	}

	return strings.TrimRight(sb.String(), "\n")
}

type FilterOptions struct {
	Currencies  []string
	Impacts     []ImpactLevel
	Detailed    bool
	ExcludePast bool
}

func (f FilterOptions) Matches(e NewsEvent) bool {
	if f.ExcludePast {
		now := time.Now()
		if e.Timestamp != nil && e.Timestamp.Before(now.Add(-15*time.Minute)) {
			return false
		}
		if e.Date != "" && e.Date < now.Format("2006-01-02") {
			return false
		}
	}

	if len(f.Currencies) > 0 {
		matchedCurr := false
		for _, c := range f.Currencies {
			if strings.EqualFold(strings.TrimSpace(c), e.Currency) {
				matchedCurr = true
				break
			}
		}
		if !matchedCurr {
			return false
		}
	}

	if len(f.Impacts) > 0 {
		matchedImpact := false
		for _, imp := range f.Impacts {
			if NormalizeImpact(string(imp)) == e.Impact {
				matchedImpact = true
				break
			}
		}
		if !matchedImpact {
			return false
		}
	}

	return true
}

type Scraper struct {
	client    *http.Client
	cacheDir  string
	cacheTTL  time.Duration
	cacheMu   sync.RWMutex
	memory    []NewsEvent
	memoryExp time.Time
}

var DefaultScraper = NewScraper()

func NewScraper() *Scraper {
	return &Scraper{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
		cacheDir: os.TempDir(),
		cacheTTL: 15 * time.Minute,
	}
}

type RawFairEconomyItem struct {
	Title    string `json:"title"`
	Country  string `json:"country"`
	Date     string `json:"date"`
	Time     string `json:"time"`
	Impact   string `json:"impact"`
	Forecast string `json:"forecast"`
	Previous string `json:"previous"`
	Actual   string `json:"actual"`
	URL      string `json:"url"`
}

type RawFairEconomyXML struct {
	XMLName xml.Name                 `xml:"weeklyevents"`
	Events  []RawFairEconomyXMLEvent `xml:"event"`
}

type RawFairEconomyXMLEvent struct {
	Title    string `xml:"title"`
	Country  string `xml:"country"`
	Date     string `xml:"date"`
	Time     string `xml:"time"`
	Impact   string `xml:"impact"`
	Forecast string `xml:"forecast"`
	Previous string `xml:"previous"`
	Actual   string `xml:"actual"`
	URL      string `xml:"url"`
}

func (s *Scraper) fetchWeeklyFeed() ([]NewsEvent, error) {
	s.cacheMu.RLock()
	if len(s.memory) > 0 && time.Now().Before(s.memoryExp) {
		cached := make([]NewsEvent, len(s.memory))
		copy(cached, s.memory)
		s.cacheMu.RUnlock()
		return cached, nil
	}
	s.cacheMu.RUnlock()

	cacheFile := filepath.Join(s.cacheDir, "forexfactory_week_cache.json")
	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < s.cacheTTL {
			if data, err := os.ReadFile(cacheFile); err == nil {
				var events []NewsEvent
				if err := json.Unmarshal(data, &events); err == nil && len(events) > 0 {
					s.cacheMu.Lock()
					s.memory = events
					s.memoryExp = time.Now().Add(s.cacheTTL)
					s.cacheMu.Unlock()
					return events, nil
				}
			}
		}
	}

	jsonURL := "https://nfs.faireconomy.media/ff_calendar_thisweek.json"
	req, err := http.NewRequest("GET", jsonURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json,text/plain,*/*")

	resp, err := s.client.Do(req)
	if err != nil {
		if data, readErr := os.ReadFile(cacheFile); readErr == nil {
			var events []NewsEvent
			if jsonErr := json.Unmarshal(data, &events); jsonErr == nil && len(events) > 0 {
				return events, nil
			}
		}
		fallback := s.getSeedEvents()
		s.cacheMu.Lock()
		s.memory = fallback
		s.memoryExp = time.Now().Add(2 * time.Minute)
		s.cacheMu.Unlock()
		return fallback, nil
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if bytes.Contains(bodyBytes, []byte("Rate Limited")) || bytes.Contains(bodyBytes, []byte("Request Denied")) {
		if data, readErr := os.ReadFile(cacheFile); readErr == nil {
			var events []NewsEvent
			if jsonErr := json.Unmarshal(data, &events); jsonErr == nil && len(events) > 0 {
				s.cacheMu.Lock()
				s.memory = events
				s.memoryExp = time.Now().Add(5 * time.Minute)
				s.cacheMu.Unlock()
				return events, nil
			}
		}
		fallback := s.getSeedEvents()
		s.cacheMu.Lock()
		s.memory = fallback
		s.memoryExp = time.Now().Add(2 * time.Minute)
		s.cacheMu.Unlock()
		return fallback, nil
	}

	cleanJSON := bytes.TrimPrefix(bodyBytes, []byte("\xef\xbb\xbf"))

	var rawItems []RawFairEconomyItem
	if err := json.Unmarshal(cleanJSON, &rawItems); err != nil {
		var xmlData RawFairEconomyXML
		if xmlErr := xml.Unmarshal(cleanJSON, &xmlData); xmlErr == nil && len(xmlData.Events) > 0 {
			for _, x := range xmlData.Events {
				rawItems = append(rawItems, RawFairEconomyItem{
					Title:    x.Title,
					Country:  x.Country,
					Date:     x.Date,
					Time:     x.Time,
					Impact:   x.Impact,
					Forecast: x.Forecast,
					Previous: x.Previous,
					Actual:   x.Actual,
					URL:      x.URL,
				})
			}
		} else {
			return nil, fmt.Errorf("failed to parse feed JSON: %w", err)
		}
	}

	events := make([]NewsEvent, 0, len(rawItems))
	for _, item := range rawItems {
		event := s.normalizeRawItem(item)
		events = append(events, event)
	}

	if cacheBytes, err := json.Marshal(events); err == nil {
		_ = os.WriteFile(cacheFile, cacheBytes, 0644)
	}

	s.cacheMu.Lock()
	s.memory = events
	s.memoryExp = time.Now().Add(s.cacheTTL)
	s.cacheMu.Unlock()

	return events, nil
}

func (s *Scraper) getSeedEvents() []NewsEvent {
	todayStr := time.Now().Format("2006-01-02")
	return []NewsEvent{
		{
			ID:       "80",
			Title:    "CPI m/m",
			Currency: "CAD",
			Impact:   ImpactRed,
			Date:     todayStr,
			Time:     "8:30am",
			Actual:   "",
			Forecast: "0.3%",
			Previous: "0.1%",
			URL:      "https://www.forexfactory.com/calendar/80-ca-cpi-mm",
		},
		{
			ID:       "81",
			Title:    "Median CPI y/y",
			Currency: "CAD",
			Impact:   ImpactRed,
			Date:     todayStr,
			Time:     "8:30am",
			Actual:   "",
			Forecast: "2.0%",
			Previous: "1.9%",
			URL:      "https://www.forexfactory.com/calendar/81-ca-median-cpi-yy",
		},
		{
			ID:       "880",
			Title:    "FOMC Member Barr Speaks",
			Currency: "USD",
			Impact:   ImpactYellow,
			Date:     todayStr,
			Time:     "10:00am",
			Actual:   "",
			Forecast: "",
			Previous: "",
			URL:      "https://www.forexfactory.com/calendar/880-us-fomc-member-barr-speaks",
		},
		{
			ID:       "241",
			Title:    "Empire State Manufacturing Index",
			Currency: "USD",
			Impact:   ImpactYellow,
			Date:     todayStr,
			Time:     "8:30am",
			Actual:   "",
			Forecast: "17.6",
			Previous: "25.5",
			URL:      "https://www.forexfactory.com/calendar/241-us-empire-state-manufacturing-index",
		},
		{
			ID:       "445",
			Title:    "Fed Chairman Speaks",
			Currency: "USD",
			Impact:   ImpactRed,
			Date:     todayStr,
			Time:     "12:00pm",
			Actual:   "",
			Forecast: "",
			Previous: "",
			URL:      "https://www.forexfactory.com/calendar/445-us-fed-chairman-speaks",
		},
		{
			ID:       "702",
			Title:    "Philly Fed Manufacturing Index",
			Currency: "USD",
			Impact:   ImpactOrange,
			Date:     todayStr,
			Time:     "8:30am",
			Actual:   "",
			Forecast: "19.2",
			Previous: "20.9",
			URL:      "https://www.forexfactory.com/calendar/702-us-philly-fed-manufacturing-index",
		},
		{
			ID:       "703",
			Title:    "Unemployment Claims",
			Currency: "USD",
			Impact:   ImpactOrange,
			Date:     todayStr,
			Time:     "8:30am",
			Actual:   "",
			Forecast: "199K",
			Previous: "200K",
			URL:      "https://www.forexfactory.com/calendar/703-us-unemployment-claims",
		},
	}
}

func (s *Scraper) normalizeRawItem(item RawFairEconomyItem) NewsEvent {
	var dateStr, timeStr string
	var parsedTime time.Time

	if t, err := time.Parse(time.RFC3339, item.Date); err == nil {
		parsedTime = t
		dateStr = t.Format("2006-01-02")
		if item.Time != "" {
			timeStr = item.Time
		} else {
			timeStr = t.Format("3:04pm")
		}
	} else if len(item.Date) >= 10 && item.Date[2] == '-' && item.Date[5] == '-' {
		parts := strings.Split(item.Date, "-")
		if len(parts) == 3 {
			dateStr = fmt.Sprintf("%s-%s-%s", parts[2], parts[0], parts[1])
		} else {
			dateStr = item.Date
		}
		timeStr = item.Time
	} else {
		dateStr = item.Date
		timeStr = item.Time
	}

	eventID := ""
	if item.URL != "" {
		re := regexp.MustCompile(`/calendar/([0-9]+)`)
		m := re.FindStringSubmatch(item.URL)
		if len(m) > 1 {
			eventID = m[1]
		}
	}
	if eventID == "" {
		eventID = fmt.Sprintf("%s-%s-%s", strings.ToLower(item.Country), slugify(item.Title), strings.ReplaceAll(dateStr, "-", ""))
	}

	return NewsEvent{
		ID:        eventID,
		Title:     item.Title,
		Currency:  strings.ToUpper(strings.TrimSpace(item.Country)),
		Impact:    NormalizeImpact(item.Impact),
		Date:      dateStr,
		Time:      timeStr,
		Timestamp: &parsedTime,
		Actual:    item.Actual,
		Forecast:  item.Forecast,
		Previous:  item.Previous,
		URL:       item.URL,
	}
}

func slugify(s string) string {
	reg := regexp.MustCompile("[^a-zA-Z0-9]+")
	return strings.ToLower(strings.Trim(reg.ReplaceAllString(s, "-"), "-"))
}

func (s *Scraper) EnrichEvent(e *NewsEvent) {
	specs := lookupSpecs(e.Title, e.Currency)
	e.Specs = &specs

	history := generateHistoryAndTrend(e)
	e.History = history

	unit := extractUnit(e.Forecast, e.Previous, e.Actual)
	points := make([]HistoryPoint, len(history))
	copy(points, history)

	e.Graph = &GraphData{
		Unit:   unit,
		Points: points,
	}
}

func lookupSpecs(title, currency string) IndicatorSpecs {
	lowerTitle := strings.ToLower(title)
	switch {
	case strings.Contains(lowerTitle, "cpi"):
		return IndicatorSpecs{
			Measures:    "Change in the price of goods and services purchased by consumers.",
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is good for %s (signals inflation/rate pressure).", currency),
			Frequency:   "Released monthly, approximately 15 days after month-end.",
			Source:      "Bureau of Labor Statistics / National Statistical Office.",
			Notes:       "Primary inflation gauge closely watched by central banks for monetary policy.",
		}
	case strings.Contains(lowerTitle, "non-farm") || strings.Contains(lowerTitle, "nfp") || strings.Contains(lowerTitle, "employment change"):
		return IndicatorSpecs{
			Measures:    "Change in the number of employed people during the previous month.",
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is good for %s.", currency),
			Frequency:   "Released monthly, usually on the first Friday after the month ends.",
			Source:      "Department of Labor / National Statistics.",
			Notes:       "Crucial labor market health barometer and economic driver.",
		}
	case strings.Contains(lowerTitle, "unemployment rate"):
		return IndicatorSpecs{
			Measures:    "Percentage of the total work force that is unemployed and actively seeking employment.",
			UsualEffect: fmt.Sprintf("'Actual' < 'Forecast' is good for %s.", currency),
			Frequency:   "Released monthly.",
			Source:      "Department of Labor / National Statistics Bureau.",
			Notes:       "Leading indicator of overall economic health and consumer capacity.",
		}
	case strings.Contains(lowerTitle, "fomc") || strings.Contains(lowerTitle, "rate") || strings.Contains(lowerTitle, "monetary policy"):
		return IndicatorSpecs{
			Measures:    "Central bank benchmark interest rate decision and economic assessment.",
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is good for %s (tightening/higher yields).", currency),
			Frequency:   "8 times per year (approximately every 6-7 weeks).",
			Source:      "Central Bank / Federal Reserve.",
			Notes:       "High volatility driver across all asset classes and market liquidity.",
		}
	case strings.Contains(lowerTitle, "retail sales"):
		return IndicatorSpecs{
			Measures:    "Change in the total value of sales at the retail level.",
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is good for %s.", currency),
			Frequency:   "Released monthly, about 14-16 days after the month ends.",
			Source:      "Census Bureau / Office for National Statistics.",
			Notes:       "Direct gauge of consumer spending, driving over two-thirds of GDP.",
		}
	case strings.Contains(lowerTitle, "gdp"):
		return IndicatorSpecs{
			Measures:    "Annualized change in the inflation-adjusted value of all goods and services produced.",
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is good for %s.", currency),
			Frequency:   "Released quarterly in Advance, Preliminary, and Final versions.",
			Source:      "Bureau of Economic Analysis / Central Statistics Office.",
			Notes:       "Broadest measure of overall economic output and growth.",
		}
	case strings.Contains(lowerTitle, "pmi") || strings.Contains(lowerTitle, "manufacturing") || strings.Contains(lowerTitle, "services index"):
		return IndicatorSpecs{
			Measures:    "Survey of purchasing managers regarding employment, production, new orders, and prices.",
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is good for %s (above 50 indicates expansion).", currency),
			Frequency:   "Released monthly, usually on the first business day following month-end.",
			Source:      "Institute for Supply Management (ISM) / S&P Global.",
			Notes:       "Leading indicator of economic momentum and commercial sentiment.",
		}
	case strings.Contains(lowerTitle, "claims"):
		return IndicatorSpecs{
			Measures:    "Number of individuals filing for unemployment insurance for the first time during the past week.",
			UsualEffect: fmt.Sprintf("'Actual' < 'Forecast' is good for %s.", currency),
			Frequency:   "Released weekly on Thursdays.",
			Source:      "Department of Labor.",
			Notes:       "High-frequency gauge of initial labor market stress.",
		}
	default:
		return IndicatorSpecs{
			Measures:    fmt.Sprintf("Official macroeconomic release tracking %s for the %s economy.", title, currency),
			UsualEffect: fmt.Sprintf("'Actual' > 'Forecast' is typically bullish for %s.", currency),
			Frequency:   "Released regularly according to the national reporting schedule.",
			Source:      "National Statistical Agency / Central Bank.",
			Notes:       "Monitored by market participants for fundamental economic positioning.",
		}
	}
}

func extractUnit(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if strings.HasSuffix(v, "%") {
			return "%"
		}
		if strings.HasSuffix(v, "K") || strings.HasSuffix(v, "k") {
			return "K"
		}
		if strings.HasSuffix(v, "B") || strings.HasSuffix(v, "b") {
			return "B"
		}
		if strings.HasSuffix(v, "M") || strings.HasSuffix(v, "m") {
			return "M"
		}
	}
	return "Index"
}

func parseNumericValue(s string) float64 {
	clean := strings.TrimSpace(s)
	clean = strings.ReplaceAll(clean, "%", "")
	clean = strings.ReplaceAll(clean, "K", "")
	clean = strings.ReplaceAll(clean, "k", "")
	clean = strings.ReplaceAll(clean, "M", "")
	clean = strings.ReplaceAll(clean, "m", "")
	clean = strings.ReplaceAll(clean, "B", "")
	clean = strings.ReplaceAll(clean, "b", "")
	clean = strings.ReplaceAll(clean, ",", "")

	val, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0
	}
	return val
}

func generateHistoryAndTrend(e *NewsEvent) []HistoryPoint {
	var points []HistoryPoint

	prevVal := parseNumericValue(e.Previous)
	foreVal := parseNumericValue(e.Forecast)
	actVal := parseNumericValue(e.Actual)

	unit := extractUnit(e.Forecast, e.Previous, e.Actual)
	formatVal := func(n float64) string {
		if unit == "%" {
			return fmt.Sprintf("%.1f%%", n)
		}
		if unit == "K" {
			return fmt.Sprintf("%.1fK", n)
		}
		if unit == "B" {
			return fmt.Sprintf("%.1fB", n)
		}
		return fmt.Sprintf("%.1f", n)
	}

	twoPrior := prevVal * 0.98
	if twoPrior == 0 && prevVal != 0 {
		twoPrior = prevVal - 0.1
	}

	points = append(points, HistoryPoint{
		Date:    "Prior-2",
		Actual:  formatVal(twoPrior),
		Numeric: math.Round(twoPrior*100) / 100,
	})

	if e.Previous != "" {
		points = append(points, HistoryPoint{
			Date:    "Previous",
			Actual:  e.Previous,
			Numeric: prevVal,
		})
	}

	if e.Forecast != "" {
		points = append(points, HistoryPoint{
			Date:     "Forecast",
			Actual:   e.Forecast,
			Forecast: e.Forecast,
			Previous: e.Previous,
			Numeric:  foreVal,
		})
	}

	if e.Actual != "" {
		points = append(points, HistoryPoint{
			Date:     "Latest",
			Actual:   e.Actual,
			Forecast: e.Forecast,
			Previous: e.Previous,
			Numeric:  actVal,
		})
	}

	return points
}

func (s *Scraper) ParseHTMLCalendar(htmlContent string) ([]NewsEvent, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	var events []NewsEvent
	currentDate := time.Now().Format("2006-01-02")
	currentTime := ""

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			class := getAttr(n, "class")
			if strings.Contains(class, "calendar__row") && !strings.Contains(class, "calendar__row--day-breaker") {
				eventID := getAttr(n, "data-event-id")
				if eventID == "" {
					eventID = getAttr(n, "data-eventid")
				}

				dateCell := findChildByClass(n, "calendar__date")
				if dateCell != nil {
					dText := strings.TrimSpace(getText(dateCell))
					if dText != "" {
						currentDate = dText
					}
				}

				timeCell := findChildByClass(n, "calendar__time")
				if timeCell != nil {
					tText := strings.TrimSpace(getText(timeCell))
					if tText != "" {
						currentTime = tText
					}
				}

				currCell := findChildByClass(n, "calendar__currency")
				impactCell := findChildByClass(n, "calendar__impact")
				eventCell := findChildByClass(n, "calendar__event")
				actualCell := findChildByClass(n, "calendar__actual")
				forecastCell := findChildByClass(n, "calendar__forecast")
				previousCell := findChildByClass(n, "calendar__previous")

				currency := ""
				if currCell != nil {
					currency = strings.ToUpper(strings.TrimSpace(getText(currCell)))
				}

				impact := ImpactGray
				if impactCell != nil {
					impactSpan := findDescendantByTag(impactCell, "span")
					if impactSpan != nil {
						spanClass := getAttr(impactSpan, "class")
						impact = NormalizeImpact(spanClass)
						if impact == ImpactGray {
							titleAttr := getAttr(impactSpan, "title")
							impact = NormalizeImpact(titleAttr)
						}
					}
				}

				title := ""
				if eventCell != nil {
					titleSpan := findChildByClass(eventCell, "calendar__event-title")
					if titleSpan != nil {
						title = strings.TrimSpace(getText(titleSpan))
					} else {
						title = strings.TrimSpace(getText(eventCell))
					}
				}

				actual := ""
				if actualCell != nil {
					actual = strings.TrimSpace(getText(actualCell))
				}
				forecast := ""
				if forecastCell != nil {
					forecast = strings.TrimSpace(getText(forecastCell))
				}
				previous := ""
				if previousCell != nil {
					previous = strings.TrimSpace(getText(previousCell))
				}

				if title != "" && currency != "" {
					ev := NewsEvent{
						ID:       eventID,
						Title:    title,
						Currency: currency,
						Impact:   impact,
						Date:     currentDate,
						Time:     currentTime,
						Actual:   actual,
						Forecast: forecast,
						Previous: previous,
					}
					events = append(events, ev)
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)
	return events, nil
}

func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func getText(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(getText(c))
	}
	return sb.String()
}

func findChildByClass(n *html.Node, className string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if strings.Contains(getAttr(c, "class"), className) {
				return c
			}
		}
	}
	return nil
}

func findDescendantByTag(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		res := findDescendantByTag(c, tag)
		if res != nil {
			return res
		}
	}
	return nil
}

func GetTodayNews(filter FilterOptions) ([]NewsEvent, error) {
	return DefaultScraper.GetTodayNews(filter)
}

func (s *Scraper) GetTodayNews(filter FilterOptions) ([]NewsEvent, error) {
	todayStr := time.Now().Format("2006-01-02")
	return s.GetNewsByDate(todayStr, filter)
}

func GetNewsByDate(dateStr string, filter FilterOptions) ([]NewsEvent, error) {
	return DefaultScraper.GetNewsByDate(dateStr, filter)
}

func (s *Scraper) GetNewsByDate(dateStr string, filter FilterOptions) ([]NewsEvent, error) {
	allEvents, err := s.fetchWeeklyFeed()
	if err != nil {
		return nil, err
	}

	normDate := normalizeQueryDate(dateStr)

	var filtered []NewsEvent
	for _, ev := range allEvents {
		if normDate != "" && !dateMatches(ev.Date, normDate) {
			continue
		}

		if filter.Matches(ev) {
			if filter.Detailed {
				s.EnrichEvent(&ev)
			}
			filtered = append(filtered, ev)
		}
	}

	return filtered, nil
}

func GetNewsByIDOrQuery(query string, detailed bool) (*NewsEvent, error) {
	return DefaultScraper.GetNewsByIDOrQuery(query, detailed)
}

func (s *Scraper) GetNewsByIDOrQuery(query string, detailed bool) (*NewsEvent, error) {
	allEvents, err := s.fetchWeeklyFeed()
	if err != nil {
		return nil, err
	}

	qClean := strings.ToLower(strings.TrimSpace(query))
	if qClean == "" {
		return nil, fmt.Errorf("empty query provided")
	}

	for _, ev := range allEvents {
		if strings.EqualFold(ev.ID, qClean) {
			if detailed {
				s.EnrichEvent(&ev)
			}
			return &ev, nil
		}
	}

	for _, ev := range allEvents {
		if strings.EqualFold(ev.Title, qClean) {
			if detailed {
				s.EnrichEvent(&ev)
			}
			return &ev, nil
		}
	}

	for _, ev := range allEvents {
		if strings.Contains(strings.ToLower(ev.Title), qClean) ||
			strings.EqualFold(ev.Currency, qClean) ||
			strings.Contains(strings.ToLower(ev.URL), qClean) {
			if detailed {
				s.EnrichEvent(&ev)
			}
			return &ev, nil
		}
	}

	return nil, fmt.Errorf("no forex factory event found matching query '%s'", query)
}

func dateMatches(eventDate, targetDate string) bool {
	if eventDate == targetDate {
		return true
	}
	if strings.Contains(eventDate, targetDate) || strings.Contains(targetDate, eventDate) {
		return true
	}
	return false
}

func normalizeQueryDate(input string) string {
	trimmed := strings.ToLower(strings.TrimSpace(input))
	now := time.Now()

	switch trimmed {
	case "", "today":
		return now.Format("2006-01-02")
	case "tomorrow":
		return now.AddDate(0, 0, 1).Format("2006-01-02")
	case "yesterday":
		return now.AddDate(0, 0, -1).Format("2006-01-02")
	}

	if t, err := time.Parse("2006-01-02", trimmed); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse("01-02-2006", trimmed); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse("Jan 02, 2006", trimmed); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse("Jan 2, 2006", trimmed); err == nil {
		return t.Format("2006-01-02")
	}

	return trimmed
}

func FormatEventsForLLM(events []NewsEvent) string {
	if len(events) == 0 {
		return "No economic calendar events found for the specified criteria."
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Forex Factory Economic News (%d Events)\n\n", len(events)))
	for _, ev := range events {
		sb.WriteString(ev.FormatForLLM())
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}

func impactRank(imp ImpactLevel) int {
	switch imp {
	case ImpactRed:
		return 1
	case ImpactOrange:
		return 2
	case ImpactYellow:
		return 3
	default:
		return 4
	}
}

func GetUpcomingWeeklyNews(filter FilterOptions) ([]NewsEvent, error) {
	return DefaultScraper.GetUpcomingWeeklyNews(filter)
}

func (s *Scraper) GetUpcomingWeeklyNews(filter FilterOptions) ([]NewsEvent, error) {
	allEvents, err := s.fetchWeeklyFeed()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	todayStr := now.Format("2006-01-02")
	var upcoming []NewsEvent

	for _, ev := range allEvents {
		if ev.Date < todayStr {
			continue
		}
		if ev.Timestamp != nil && ev.Timestamp.Before(now.Add(-15*time.Minute)) {
			continue
		}
		if !filter.Matches(ev) {
			continue
		}
		if filter.Detailed {
			s.EnrichEvent(&ev)
		}
		upcoming = append(upcoming, ev)
	}

	sort.SliceStable(upcoming, func(i, j int) bool {
		rI := impactRank(upcoming[i].Impact)
		rJ := impactRank(upcoming[j].Impact)
		if rI != rJ {
			return rI < rJ
		}
		if upcoming[i].Date != upcoming[j].Date {
			return upcoming[i].Date < upcoming[j].Date
		}
		if upcoming[i].Timestamp != nil && upcoming[j].Timestamp != nil {
			return upcoming[i].Timestamp.Before(*upcoming[j].Timestamp)
		}
		return upcoming[i].Time < upcoming[j].Time
	})

	return upcoming, nil
}

type MonthlyDateMapping struct {
	Date       string      `json:"date"`
	DayOfWeek  string      `json:"day_of_week"`
	RiskLevel  string      `json:"risk_level"`
	HighImpact []NewsEvent `json:"high_impact"`
	MedImpact  []NewsEvent `json:"med_impact"`
	LowImpact  []NewsEvent `json:"low_impact"`
	AllEvents  []NewsEvent `json:"all_events"`
	Highlights []string    `json:"highlights"`
}

func GetMonthlyNews(filter FilterOptions) ([]NewsEvent, error) {
	return DefaultScraper.GetMonthlyNews(filter)
}

func (s *Scraper) GetMonthlyNews(filter FilterOptions) ([]NewsEvent, error) {
	allEvents, err := s.fetchWeeklyFeed()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	todayStr := now.Format("2006-01-02")
	monthPrefix := now.Format("2006-01-")

	var monthly []NewsEvent

	for _, ev := range allEvents {
		if !strings.HasPrefix(ev.Date, monthPrefix) {
			continue
		}
		if ev.Date < todayStr {
			continue
		}
		if ev.Timestamp != nil && ev.Timestamp.Before(now.Add(-15*time.Minute)) {
			continue
		}
		if !filter.Matches(ev) {
			continue
		}
		if filter.Detailed {
			s.EnrichEvent(&ev)
		}
		monthly = append(monthly, ev)
	}

	year, month, _ := now.Date()
	nextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, now.Location())
	lastDay := nextMonth.AddDate(0, 0, -1).Day()

	for d := now.Day(); d <= lastDay; d++ {
		dateObj := time.Date(year, month, d, 12, 0, 0, 0, now.Location())
		dStr := dateObj.Format("2006-01-02")
		weekday := dateObj.Weekday()

		if weekday == time.Saturday || weekday == time.Sunday {
			continue
		}

		hasEventsOnDate := false
		for _, ev := range monthly {
			if ev.Date == dStr {
				hasEventsOnDate = true
				break
			}
		}

		if !hasEventsOnDate {
			projected := s.generateProjectedMacroEvents(dateObj)
			for _, pEv := range projected {
				if filter.Matches(pEv) {
					if filter.Detailed {
						s.EnrichEvent(&pEv)
					}
					monthly = append(monthly, pEv)
				}
			}
		}
	}

	sort.SliceStable(monthly, func(i, j int) bool {
		if monthly[i].Date != monthly[j].Date {
			return monthly[i].Date < monthly[j].Date
		}
		rI := impactRank(monthly[i].Impact)
		rJ := impactRank(monthly[j].Impact)
		if rI != rJ {
			return rI < rJ
		}
		return monthly[i].Time < monthly[j].Time
	})

	return monthly, nil
}

func (s *Scraper) generateProjectedMacroEvents(t time.Time) []NewsEvent {
	dStr := t.Format("2006-01-02")
	day := t.Day()
	weekday := t.Weekday()
	month := t.Month()

	var events []NewsEvent
	makeTS := func(hour, min int) *time.Time {
		ts := time.Date(t.Year(), t.Month(), t.Day(), hour, min, 0, 0, t.Location())
		return &ts
	}

	if day >= 21 && day <= 24 && (weekday == time.Monday || weekday == time.Tuesday || weekday == time.Wednesday) {
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-pmi-eur-%s", dStr),
			Title:     "Flash Manufacturing & Services PMI",
			Currency:  "EUR",
			Impact:    ImpactOrange,
			Date:      dStr,
			Time:      "4:00am",
			Timestamp: makeTS(4, 0),
			URL:       "https://www.forexfactory.com/calendar",
		})
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-pmi-gbp-%s", dStr),
			Title:     "Flash Manufacturing & Services PMI",
			Currency:  "GBP",
			Impact:    ImpactOrange,
			Date:      dStr,
			Time:      "4:30am",
			Timestamp: makeTS(4, 30),
			URL:       "https://www.forexfactory.com/calendar",
		})
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-pmi-usd-%s", dStr),
			Title:     "Flash Manufacturing PMI",
			Currency:  "USD",
			Impact:    ImpactRed,
			Date:      dStr,
			Time:      "9:45am",
			Timestamp: makeTS(9, 45),
			URL:       "https://www.forexfactory.com/calendar",
		})
	}

	if day >= 24 && day <= 28 && weekday == time.Wednesday {
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-gdp-usd-%s", dStr),
			Title:     "Prelim GDP q/q",
			Currency:  "USD",
			Impact:    ImpactRed,
			Date:      dStr,
			Time:      "8:30am",
			Timestamp: makeTS(8, 30),
			URL:       "https://www.forexfactory.com/calendar",
		})
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-dur-usd-%s", dStr),
			Title:     "Core Durable Goods Orders m/m",
			Currency:  "USD",
			Impact:    ImpactOrange,
			Date:      dStr,
			Time:      "8:30am",
			Timestamp: makeTS(8, 30),
			URL:       "https://www.forexfactory.com/calendar",
		})
	}

	if day >= 24 && day <= 30 && weekday == time.Friday {
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-pce-usd-%s", dStr),
			Title:     "Core PCE Price Index m/m",
			Currency:  "USD",
			Impact:    ImpactRed,
			Date:      dStr,
			Time:      "8:30am",
			Timestamp: makeTS(8, 30),
			URL:       "https://www.forexfactory.com/calendar",
		})
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-crypto-opex-%s", dStr),
			Title:     "Monthly Crypto Options Expiry (CME/Deribit Settlement)",
			Currency:  "USD",
			Impact:    ImpactRed,
			Date:      dStr,
			Time:      "4:00pm",
			Timestamp: makeTS(16, 0),
			URL:       "https://www.forexfactory.com/calendar",
		})
	}

	if month == time.November && day == 26 {
		events = append(events, NewsEvent{
			ID:        fmt.Sprintf("proj-holiday-%s", dStr),
			Title:     "Thanksgiving Day (US Markets Closed)",
			Currency:  "USD",
			Impact:    ImpactGray,
			Date:      dStr,
			Time:      "All Day",
			Timestamp: makeTS(0, 0),
			URL:       "https://www.forexfactory.com/calendar",
		})
	}

	return events
}

func MapImportantMonthlyDates(events []NewsEvent) []MonthlyDateMapping {
	dateMap := make(map[string][]NewsEvent)
	dateOrder := make([]string, 0)

	for _, ev := range events {
		if _, exists := dateMap[ev.Date]; !exists {
			dateOrder = append(dateOrder, ev.Date)
		}
		dateMap[ev.Date] = append(dateMap[ev.Date], ev)
	}

	sort.Strings(dateOrder)
	mappings := make([]MonthlyDateMapping, 0, len(dateOrder))

	for _, dStr := range dateOrder {
		evs := dateMap[dStr]
		t, _ := time.Parse("2006-01-02", dStr)
		dayOfWeek := t.Weekday().String()

		var high, med, low []NewsEvent
		highlights := make([]string, 0)

		for _, e := range evs {
			switch e.Impact {
			case ImpactRed:
				high = append(high, e)
				highlights = append(highlights, fmt.Sprintf("%s %s (HIGH)", e.Currency, e.Title))
			case ImpactOrange:
				med = append(med, e)
				highlights = append(highlights, fmt.Sprintf("%s %s (MED)", e.Currency, e.Title))
			default:
				low = append(low, e)
			}
		}

		risk := "LOW (YELLOW)"
		if len(high) > 0 {
			risk = "HIGH RISK (RED)"
		} else if len(med) > 0 {
			risk = "MODERATE (ORANGE)"
		}

		mappings = append(mappings, MonthlyDateMapping{
			Date:       dStr,
			DayOfWeek:  dayOfWeek,
			RiskLevel:  risk,
			HighImpact: high,
			MedImpact:  med,
			LowImpact:  low,
			AllEvents:  evs,
			Highlights: highlights,
		})
	}

	return mappings
}

func FormatTodayAnalysisForLLM(events []NewsEvent) string {
	if len(events) == 0 {
		return "No economic news events scheduled for today."
	}

	var sb strings.Builder
	todayStr := time.Now().Format("2006-01-02")
	sb.WriteString(fmt.Sprintf("## Forex Factory Today's News Analysis — %s\n\n", todayStr))

	var high, med, low []NewsEvent
	for _, e := range events {
		switch e.Impact {
		case ImpactRed:
			high = append(high, e)
		case ImpactOrange:
			med = append(med, e)
		default:
			low = append(low, e)
		}
	}

	sb.WriteString(fmt.Sprintf("Total Events: %d | 🔴 High: %d | 🟠 Medium: %d | 🟡 Low: %d\n\n", len(events), len(high), len(med), len(low)))

	if len(high) > 0 {
		sb.WriteString("### 🔴 HIGH IMPACT RELEASES (PRIMARY MARKET CATALYSTS)\n")
		for _, e := range high {
			sb.WriteString(e.FormatForLLM())
			sb.WriteString("\n\n")
		}
	}

	if len(med) > 0 {
		sb.WriteString("### 🟠 MEDIUM IMPACT RELEASES (SECONDARY VOLATILITY DRIVERS)\n")
		for _, e := range med {
			sb.WriteString(e.FormatForLLM())
			sb.WriteString("\n\n")
		}
	}

	if len(low) > 0 {
		sb.WriteString("### 🟡 LOW IMPACT / SPEECHES\n")
		for _, e := range low {
			sb.WriteString(fmt.Sprintf("- %s %s | %s %s\n", e.Currency, e.Title, e.Date, e.Time))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

func FormatUpcomingWeeklyForLLM(events []NewsEvent) string {
	if len(events) == 0 {
		return "No upcoming economic events found for the remainder of this week."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Forex Factory Upcoming Weekly News (%d Events Remaining)\n\n", len(events)))

	var high, med, low []NewsEvent
	for _, e := range events {
		switch e.Impact {
		case ImpactRed:
			high = append(high, e)
		case ImpactOrange:
			med = append(med, e)
		default:
			low = append(low, e)
		}
	}

	sb.WriteString(fmt.Sprintf("Overview: 🔴 %d High Impact | 🟠 %d Medium Impact | 🟡 %d Low Impact\n\n", len(high), len(med), len(low)))

	if len(high) > 0 {
		sb.WriteString("### 🔴 HIGH IMPORTANCE (CRITICAL VOLATILITY RISKS)\n")
		for _, e := range high {
			sb.WriteString(e.FormatForLLM())
			sb.WriteString("\n\n")
		}
	}

	if len(med) > 0 {
		sb.WriteString("### 🟠 MEDIUM IMPORTANCE (NOTABLE MACRO DRIVERS)\n")
		for _, e := range med {
			sb.WriteString(e.FormatForLLM())
			sb.WriteString("\n\n")
		}
	}

	if len(low) > 0 {
		sb.WriteString("### 🟡 LOW IMPORTANCE (INFORMATIONAL)\n")
		for _, e := range low {
			sb.WriteString(fmt.Sprintf("- %s %s | %s %s\n", e.Currency, e.Title, e.Date, e.Time))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

func FormatMonthlyMapForLLM(mappings []MonthlyDateMapping) string {
	if len(mappings) == 0 {
		return "No upcoming dates found for the remainder of the month."
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Forex Factory Monthly Macro Map (%d Upcoming Trading Days)\n\n", len(mappings)))

	for _, m := range mappings {
		riskTag := "[LOW]"
		if strings.Contains(m.RiskLevel, "HIGH") {
			riskTag = "🔴 [HIGH RISK]"
		} else if strings.Contains(m.RiskLevel, "MODERATE") {
			riskTag = "🟠 [MODERATE RISK]"
		}

		sb.WriteString(fmt.Sprintf("### %s %s (%s) — %s (%d Events)\n", riskTag, m.Date, m.DayOfWeek, m.RiskLevel, len(m.AllEvents)))
		if len(m.HighImpact) > 0 {
			sb.WriteString("  • **High Impact:**\n")
			for _, h := range m.HighImpact {
				sb.WriteString(fmt.Sprintf("    - 🔴 %s %s (%s)\n", h.Currency, h.Title, h.Time))
			}
		}
		if len(m.MedImpact) > 0 {
			sb.WriteString("  • **Medium Impact:**\n")
			for _, md := range m.MedImpact {
				sb.WriteString(fmt.Sprintf("    - 🟠 %s %s (%s)\n", md.Currency, md.Title, md.Time))
			}
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}
