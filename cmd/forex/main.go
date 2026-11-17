package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"trader-skill/internal/macro"
)

type Output struct {
	Mode           string                     `json:"mode"`
	TotalEvents    int                        `json:"total_events"`
	Events         []macro.NewsEvent          `json:"events,omitempty"`
	MonthlyMapping []macro.MonthlyDateMapping `json:"monthly_mapping,omitempty"`
	FormattedText  string                     `json:"formatted_text,omitempty"`
}

func main() {
	modeFlag := flag.String("mode", "analysis", "Mode: analysis, today, weekly, monthly")
	jsonFlag := flag.Bool("json", false, "Output pure JSON")
	currencyFlag := flag.String("currency", "", "Comma-separated currency filter")
	impactFlag := flag.String("impact", "", "Comma-separated impact filter (red, orange, yellow)")
	detailedFlag := flag.Bool("detailed", true, "Include detailed indicator specs and trends")
	flag.Parse()

	filter := macro.FilterOptions{
		Detailed: *detailedFlag,
	}

	if *currencyFlag != "" {
		for _, c := range strings.Split(*currencyFlag, ",") {
			cClean := strings.TrimSpace(c)
			if cClean != "" {
				filter.Currencies = append(filter.Currencies, cClean)
			}
		}
	}

	if *impactFlag != "" {
		for _, imp := range strings.Split(*impactFlag, ",") {
			impClean := strings.TrimSpace(imp)
			if impClean != "" {
				filter.Impacts = append(filter.Impacts, macro.NormalizeImpact(impClean))
			}
		}
	}

	mode := strings.ToLower(strings.TrimSpace(*modeFlag))

	var out Output
	out.Mode = mode

	switch mode {
	case "analysis", "today":
		events, err := macro.GetTodayNews(filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching today news: %v\n", err)
			os.Exit(1)
		}
		out.TotalEvents = len(events)
		out.Events = events
		out.FormattedText = macro.FormatTodayAnalysisForLLM(events)

		if *jsonFlag {
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Println(out.FormattedText)
		}

	case "weekly", "week":
		filter.ExcludePast = true
		events, err := macro.GetUpcomingWeeklyNews(filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching weekly news: %v\n", err)
			os.Exit(1)
		}
		out.TotalEvents = len(events)
		out.Events = events
		out.FormattedText = macro.FormatUpcomingWeeklyForLLM(events)

		if *jsonFlag {
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Println(out.FormattedText)
		}

	case "monthly", "month":
		filter.ExcludePast = true
		events, err := macro.GetMonthlyNews(filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error fetching monthly news: %v\n", err)
			os.Exit(1)
		}
		mappings := macro.MapImportantMonthlyDates(events)
		out.TotalEvents = len(events)
		out.Events = events
		out.MonthlyMapping = mappings
		out.FormattedText = macro.FormatMonthlyMapForLLM(mappings)

		if *jsonFlag {
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(data))
		} else {
			fmt.Println(out.FormattedText)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown mode '%s', expected: analysis, weekly, monthly\n", mode)
		os.Exit(1)
	}
}
