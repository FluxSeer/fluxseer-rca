package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FluxSeer/fluxseer-rca/internal/skillqualifier"
)

func main() {
	casesPath := flag.String("cases", "test/skill/engineering-baseline-evals.yaml", "behavioral eval corpus")
	var resultsPaths stringListFlag
	flag.Var(&resultsPaths, "results", "structured Agent capture results; repeat for stability runs")
	outputPath := flag.String("output", "", "optional JSON report path")
	flag.Parse()

	corpus, err := skillqualifier.LoadCorpus(*casesPath)
	if err != nil {
		fail(err)
	}

	report := skillqualifier.PreparedReport(corpus)
	var aggregate *skillqualifier.AggregateReport
	if len(resultsPaths) > 0 {
		runs := make([]skillqualifier.CapturedRun, 0, len(resultsPaths))
		for _, resultsPath := range resultsPaths {
			run, loadErr := skillqualifier.LoadCapturedRun(resultsPath)
			if loadErr != nil {
				fail(loadErr)
			}
			runs = append(runs, run)
		}
		if len(runs) == 1 {
			report, err = skillqualifier.Evaluate(corpus, runs[0])
			if err != nil {
				fail(err)
			}
		} else {
			result, aggregateErr := skillqualifier.EvaluateRuns(corpus, runs)
			if aggregateErr != nil {
				fail(aggregateErr)
			}
			aggregate = &result
		}
	}

	var data []byte
	if aggregate != nil {
		data, err = skillqualifier.MarshalAggregateReport(*aggregate)
	} else {
		data, err = skillqualifier.MarshalReport(report)
	}
	if err != nil {
		fail(fmt.Errorf("marshal qualification report: %w", err))
	}
	if *outputPath != "" {
		if err := os.WriteFile(*outputPath, data, 0o644); err != nil {
			fail(fmt.Errorf("write qualification report: %w", err))
		}
	} else {
		fmt.Println(string(data))
	}
	if aggregate != nil && aggregate.Status != skillqualifier.StatusPass {
		os.Exit(1)
	}
	if aggregate == nil && report.ExecutionStatus == "EXECUTED" && report.Status != skillqualifier.StatusPass {
		os.Exit(1)
	}
}

type stringListFlag []string

func (items *stringListFlag) String() string {
	return fmt.Sprint([]string(*items))
}

func (items *stringListFlag) Set(value string) error {
	*items = append(*items, value)
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
