package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var (
	verbose        bool
	ignorePatterns []string
)

func main() {
	// Parse our own flags
	args := parseFlags(os.Args[1:])

	// Check for help flag
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "-help" {
			printUsage()
			return
		}
	}

	if err := run(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags extracts gotest-specific flags and returns remaining args for go test
func parseFlags(args []string) []string {
	var goTestArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-d" || arg == "--detail" || arg == "-detail":
			verbose = true
		case arg == "-i" || arg == "--ignore" || arg == "-ignore":
			// Next arg should be the patterns
			if i+1 < len(args) {
				i++
				patterns := strings.Split(args[i], ",")
				for _, p := range patterns {
					p = strings.TrimSpace(p)
					if p != "" {
						ignorePatterns = append(ignorePatterns, p)
					}
				}
			}
		case strings.HasPrefix(arg, "-i=") || strings.HasPrefix(arg, "--ignore=") || strings.HasPrefix(arg, "-ignore="):
			// Handle -i=pattern,pattern format
			var value string
			if strings.HasPrefix(arg, "-i=") {
				value = arg[3:]
			} else if strings.HasPrefix(arg, "--ignore=") {
				value = arg[9:]
			} else {
				value = arg[8:]
			}
			patterns := strings.Split(value, ",")
			for _, p := range patterns {
				p = strings.TrimSpace(p)
				if p != "" {
					ignorePatterns = append(ignorePatterns, p)
				}
			}
		default:
			goTestArgs = append(goTestArgs, arg)
		}
	}
	return goTestArgs
}

func printUsage() {
	fmt.Println(`gotest - Run go test recursively with coverage

Usage:
  gotest [options] [go test flags...]

Options:
  -d, --detail              Show detailed test output (default: minimal output)
  -i, --ignore <patterns>   Ignore packages matching patterns (comma-separated)
  -h, --help                Show this help message

Description:
  Automatically finds all Go packages in the current directory and
  subdirectories, runs 'go test' with coverage, displays coverage
  statistics, and opens the HTML report in your browser.

  Recursion is automatic - no flags needed!

Examples:
  gotest                              Run all tests (minimal output)
  gotest -d                           Run with detailed output
  gotest -i example,pb                Ignore packages containing "example" or "pb"
  gotest --ignore=cmd,testdata        Same as above with = syntax
  gotest -i generated -v              Ignore + verbose go test output
  gotest -run TestFoo                 Run specific tests

Output:
  Coverage profile: /tmp/cover.out
  HTML report:      /tmp/cover.html

All other flags are passed directly to 'go test'. See 'go help test' for details.`)
}

func run(userArgs []string) error {
	// Find all directories containing .go files
	packages, err := findGoPackages(".")
	if err != nil {
		return fmt.Errorf("finding go packages: %w", err)
	}

	if len(packages) == 0 {
		fmt.Println("No Go packages found")
		return nil
	}

	if verbose {
		fmt.Printf("Found %d package(s) with Go files:\n", len(packages))
		for _, pkg := range packages {
			fmt.Printf("  - %s\n", pkg)
		}
		fmt.Println()
	} else {
		fmt.Printf("Testing %d package(s)...\n", len(packages))
	}

	// Coverage output file
	coverProfile := "/tmp/cover.out"
	coverHTML := "/tmp/cover.html"

	// Build go test arguments
	args := []string{"test"}

	// Add coverage flags
	args = append(args, "-coverprofile="+coverProfile, "-covermode=atomic")

	// Add user-provided arguments
	args = append(args, userArgs...)

	// Add all packages to test
	args = append(args, packages...)

	// Run go test
	if verbose {
		fmt.Printf("Running: go %s\n\n", strings.Join(args, " "))
	}

	cmd := exec.Command("go", args...)

	var testOutput bytes.Buffer
	var testErr error

	if verbose {
		// In verbose mode, stream output directly
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		testErr = cmd.Run()
	} else {
		// In quiet mode, capture output and only show errors
		cmd.Stdout = &testOutput
		cmd.Stderr = &testOutput
		testErr = cmd.Run()

		// Only show output if there were errors
		if testErr != nil {
			fmt.Println("\n--- TEST ERRORS ---")
			// Filter output to show only failures
			printTestErrors(testOutput.String())
			fmt.Println("-------------------")
		}
	}

	if testErr != nil {
		fmt.Fprintf(os.Stderr, "\nTests failed\n")
	} else {
		fmt.Println("All tests passed")
	}

	// Check if coverage profile was generated
	if _, err := os.Stat(coverProfile); os.IsNotExist(err) {
		return fmt.Errorf("coverage profile not generated at %s", coverProfile)
	}

	// Parse and display coverage statistics
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("COVERAGE SUMMARY")
	fmt.Println(strings.Repeat("=", 60))

	if err := displayCoverageStats(coverProfile); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not parse coverage stats: %v\n", err)
	}

	fmt.Println(strings.Repeat("=", 60))

	// Generate HTML coverage report
	if verbose {
		fmt.Printf("\nGenerating coverage report: %s\n", coverHTML)
	}
	coverCmd := exec.Command("go", "tool", "cover", "-html="+coverProfile, "-o", coverHTML)
	if verbose {
		coverCmd.Stdout = os.Stdout
		coverCmd.Stderr = os.Stderr
	}

	if err := coverCmd.Run(); err != nil {
		return fmt.Errorf("generating coverage HTML: %w", err)
	}

	// Open coverage report in browser
	fmt.Printf("\nOpening %s in browser...\n", coverHTML)
	if err := openBrowser(coverHTML); err != nil {
		return fmt.Errorf("opening browser: %w", err)
	}

	return nil
}

// printTestErrors filters and prints only error-related output
func printTestErrors(output string) {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// Show FAIL lines, error messages, and panic info
		if strings.Contains(line, "FAIL") ||
			strings.Contains(line, "Error") ||
			strings.Contains(line, "error") ||
			strings.Contains(line, "panic") ||
			strings.Contains(line, "--- FAIL") ||
			strings.HasPrefix(strings.TrimSpace(line), "got:") ||
			strings.HasPrefix(strings.TrimSpace(line), "want:") ||
			strings.HasPrefix(strings.TrimSpace(line), "expected") ||
			strings.Contains(line, "_test.go:") {
			fmt.Println(line)
		}
	}
}

// CoverageStats holds coverage statistics for a package
type CoverageStats struct {
	TotalStatements   int
	CoveredStatements int
}

// displayCoverageStats parses the coverage profile and displays per-package and total coverage
func displayCoverageStats(coverProfile string) error {
	file, err := os.Open(coverProfile)
	if err != nil {
		return err
	}
	defer file.Close()

	// Map of package path to coverage stats
	packageStats := make(map[string]*CoverageStats)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// Skip mode line
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		// Parse coverage line: file:startLine.startCol,endLine.endCol numStatements count
		// Example: github.com/user/pkg/file.go:10.2,12.16 3 1
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}

		// Extract file path (before the colon with line numbers)
		filePart := parts[0]
		colonIdx := strings.LastIndex(filePart, ":")
		if colonIdx == -1 {
			continue
		}
		filePath := filePart[:colonIdx]

		// Get package path (directory of the file)
		pkgPath := filepath.Dir(filePath)

		// Parse number of statements
		numStatements, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		// Parse count (how many times executed)
		count, err := strconv.Atoi(parts[2])
		if err != nil {
			continue
		}

		// Initialize package stats if needed
		if packageStats[pkgPath] == nil {
			packageStats[pkgPath] = &CoverageStats{}
		}

		packageStats[pkgPath].TotalStatements += numStatements
		if count > 0 {
			packageStats[pkgPath].CoveredStatements += numStatements
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if len(packageStats) == 0 {
		fmt.Println("No coverage data found")
		return nil
	}

	// Sort packages for consistent output
	var pkgNames []string
	for pkg := range packageStats {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	// Display header
	fmt.Println()
	fmt.Printf("%-61s %10s\n", "PACKAGE", "COVERAGE")
	fmt.Println(strings.Repeat("-", 70))

	// Calculate and display per-package coverage
	var totalStatements, totalCovered int

	for _, pkg := range pkgNames {
		stats := packageStats[pkg]
		totalStatements += stats.TotalStatements
		totalCovered += stats.CoveredStatements

		var coverage float64
		if stats.TotalStatements > 0 {
			coverage = float64(stats.CoveredStatements) / float64(stats.TotalStatements) * 100
		}

		// Truncate long package names with "..." prefix
		displayPkg := pkg
		if len(displayPkg) > 58 {
			displayPkg = "..." + displayPkg[len(displayPkg)-55:]
		}

		fmt.Printf("%-61s %8.1f%%\n", displayPkg, coverage)
	}

	// Display total
	fmt.Println(strings.Repeat("-", 70))

	var totalCoverage float64
	if totalStatements > 0 {
		totalCoverage = float64(totalCovered) / float64(totalStatements) * 100
	}

	fmt.Printf("%-61s %8.1f%%\n", "TOTAL", totalCoverage)
	fmt.Printf("\nStatements: %d/%d covered\n", totalCovered, totalStatements)

	return nil
}

// findGoPackages finds all directories containing .go files (excluding test files only dirs)
func findGoPackages(root string) ([]string, error) {
	var packages []string
	seen := make(map[string]bool)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and common non-source directories
		if info.IsDir() {
			name := info.Name()
			// Skip hidden dirs (but not "." which is the root), vendor, and testdata
			if (strings.HasPrefix(name, ".") && name != ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}

			// Skip directories matching ignore patterns
			if shouldIgnore(path) {
				return filepath.SkipDir
			}
			return nil
		}

		// Check for .go files (including test files)
		if strings.HasSuffix(path, ".go") {
			dir := filepath.Dir(path)
			if !seen[dir] && !shouldIgnore(dir) {
				seen[dir] = true
				// Convert to package path format
				if dir == "." {
					packages = append(packages, "./.")
				} else {
					packages = append(packages, "./"+dir)
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return packages, nil
}

// openBrowser opens the specified URL in the default browser
func openBrowser(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}
