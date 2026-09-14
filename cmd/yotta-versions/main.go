// Command yotta-versions owns repository product-version projections and
// reports the independently evolving compatibility-version inventory.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	installedautomation "github.com/yottaapp/yotta/internal/automation/installed"
	"github.com/yottaapp/yotta/internal/blob"
	"github.com/yottaapp/yotta/internal/capability"
	"github.com/yottaapp/yotta/internal/datatype"
	"github.com/yottaapp/yotta/internal/hostapi"
	"github.com/yottaapp/yotta/internal/nodeauthoring"
	"github.com/yottaapp/yotta/internal/nodecatalog"
	"github.com/yottaapp/yotta/internal/nodecontract"
	"github.com/yottaapp/yotta/internal/nodepackage"
	"github.com/yottaapp/yotta/internal/nodes"
	"github.com/yottaapp/yotta/internal/pluginprotocol"
	"github.com/yottaapp/yotta/internal/releasecompat"
	runartifact "github.com/yottaapp/yotta/internal/run"
	"github.com/yottaapp/yotta/internal/scriptengine"
	"github.com/yottaapp/yotta/internal/services"
	"github.com/yottaapp/yotta/internal/services/asset"
	"github.com/yottaapp/yotta/internal/services/inputclip"
	"github.com/yottaapp/yotta/internal/services/macro"
	"github.com/yottaapp/yotta/internal/services/mcpserver"
	"github.com/yottaapp/yotta/internal/services/schedule"
	"github.com/yottaapp/yotta/internal/services/snippet"
	"github.com/yottaapp/yotta/internal/storage"
	"github.com/yottaapp/yotta/internal/storage/catalog"
	storagemigrate "github.com/yottaapp/yotta/internal/storage/migrate"
	"github.com/yottaapp/yotta/internal/workflow/compiler"
	"github.com/yottaapp/yotta/internal/workflow/schema"
	"github.com/yottaapp/yotta/internal/workflowbundle"
	"github.com/yottaapp/yotta/internal/workflowstore"
)

const productVersionVariable = "github.com/yottaapp/yotta/pkg/version.Version"

var productSemverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-alpha\.([1-9][0-9]*))?$`)

type productVersion struct {
	Major int
	Minor int
	Patch int
	Alpha int
}

type inventoryRow struct {
	name             string
	version          string
	class            string
	readableVersions []string
}

func (v productVersion) String() string {
	if v.Alpha > 0 {
		return fmt.Sprintf("%d.%d.%d-alpha.%d", v.Major, v.Minor, v.Patch, v.Alpha)
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v productVersion) WindowsManifest() string {
	return fmt.Sprintf("%d.%d.%d.%d", v.Major, v.Minor, v.Patch, v.Alpha)
}

func parseProductVersion(value string) (productVersion, error) {
	match := productSemverPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return productVersion{}, fmt.Errorf("product version must be MAJOR.MINOR.PATCH or MAJOR.MINOR.PATCH-alpha.N, got %q", value)
	}
	parts := [3]int{}
	for index := range parts {
		parsed, err := strconv.Atoi(match[index+1])
		if err != nil {
			return productVersion{}, fmt.Errorf("parse product version %q: %w", value, err)
		}
		parts[index] = parsed
	}
	alpha := 0
	if match[4] != "" {
		parsed, err := strconv.Atoi(match[4])
		if err != nil {
			return productVersion{}, fmt.Errorf("parse product version %q: %w", value, err)
		}
		alpha = parsed
	}
	if parts[0] > 255 || parts[1] > 255 || parts[2] > 65535 || alpha > 65535 {
		return productVersion{}, errors.New("product version exceeds Windows installer numeric limits")
	}
	return productVersion{Major: parts[0], Minor: parts[1], Patch: parts[2], Alpha: alpha}, nil
}

type projection struct {
	Path      string
	Transform func([]byte, productVersion) ([]byte, error)
}

var productProjections = []projection{
	{Path: "build/config.yml", Transform: projectWailsConfig},
	{Path: "build/windows/info.json", Transform: projectWindowsInfo},
	{Path: "build/windows/nsis/wails_tools.nsh", Transform: projectNSIS},
	{Path: "build/windows/wails.exe.manifest", Transform: projectWindowsManifest},
	{Path: "build/windows/wails.dev.manifest", Transform: projectWindowsManifest},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("usage: yotta-versions <show|check|sync|bump|inventory|compatibility|ldflag>")
	}
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	current, err := readProductVersion(root)
	if err != nil {
		return err
	}
	switch arguments[0] {
	case "show":
		if len(arguments) != 1 {
			return errors.New("usage: yotta-versions show")
		}
		fmt.Println(current)
		return nil
	case "ldflag":
		if len(arguments) != 1 {
			return errors.New("usage: yotta-versions ldflag")
		}
		fmt.Printf("-X %s=%s\n", productVersionVariable, current)
		return nil
	case "check":
		if len(arguments) != 1 {
			return errors.New("usage: yotta-versions check")
		}
		changed, err := syncProductProjections(root, current, false)
		if err != nil {
			return err
		}
		if len(changed) != 0 {
			return fmt.Errorf("product version projections are stale: %s; run task version:sync", strings.Join(changed, ", "))
		}
		if err := checkStoragePathOwnership(root); err != nil {
			return err
		}
		fmt.Printf("product version projections OK: %s\n", current)
		return nil
	case "sync":
		if len(arguments) != 1 {
			return errors.New("usage: yotta-versions sync")
		}
		changed, err := syncProductProjections(root, current, true)
		if err != nil {
			return err
		}
		if len(changed) == 0 {
			fmt.Printf("product version projections already current: %s\n", current)
			return nil
		}
		fmt.Printf("updated product version projections to %s: %s\n", current, strings.Join(changed, ", "))
		return nil
	case "bump":
		return bumpProductVersion(root, current, arguments[1:])
	case "inventory":
		if len(arguments) != 1 {
			return errors.New("usage: yotta-versions inventory")
		}
		printInventory(current)
		return nil
	case "compatibility":
		return runVersionDomainCompatibility(root, current, arguments[1:])
	default:
		return fmt.Errorf("unknown yotta-versions command %q", arguments[0])
	}
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(current, "VERSION")) && fileExists(filepath.Join(current, "go.mod")) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository root containing VERSION and go.mod was not found")
		}
		current = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func readProductVersion(root string) (productVersion, error) {
	raw, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return productVersion{}, fmt.Errorf("read VERSION: %w", err)
	}
	if bytes.ContainsAny(bytes.TrimSpace(raw), " \t\r\n") {
		return productVersion{}, errors.New("VERSION must contain exactly one supported SemVer")
	}
	return parseProductVersion(string(raw))
}

func syncProductProjections(root string, version productVersion, write bool) ([]string, error) {
	changed := make([]string, 0, len(productProjections))
	type pendingWrite struct {
		path string
		raw  []byte
		mode os.FileMode
	}
	pending := make([]pendingWrite, 0, len(productProjections))
	for _, item := range productProjections {
		path := filepath.Join(root, filepath.FromSlash(item.Path))
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat product version projection %s: %w", item.Path, err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read product version projection %s: %w", item.Path, err)
		}
		projected, err := item.Transform(raw, version)
		if err != nil {
			return nil, fmt.Errorf("project product version into %s: %w", item.Path, err)
		}
		if bytes.Equal(raw, projected) {
			continue
		}
		changed = append(changed, item.Path)
		pending = append(pending, pendingWrite{path: path, raw: projected, mode: info.Mode().Perm()})
	}
	if !write {
		return changed, nil
	}
	for _, item := range pending {
		if err := os.WriteFile(item.path, item.raw, item.mode); err != nil {
			return nil, fmt.Errorf("write product version projection %s: %w", item.path, err)
		}
	}
	return changed, nil
}

func bumpProductVersion(root string, current productVersion, arguments []string) error {
	flags := flag.NewFlagSet("bump", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "report without writing")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: yotta-versions bump [--dry-run] <alpha|patch|minor|major|MAJOR.MINOR.PATCH[-alpha.N]>")
	}
	target, err := nextProductVersion(current, flags.Arg(0))
	if err != nil {
		return err
	}
	if _, err := parseProductVersion(target.String()); err != nil {
		return err
	}
	if target == current {
		return fmt.Errorf("product version is already %s", current)
	}
	if *dryRun {
		fmt.Printf("would bump product version %s -> %s and refresh %d projections\n", current, target, len(productProjections))
		return nil
	}
	versionPath := filepath.Join(root, "VERSION")
	if err := os.WriteFile(versionPath, []byte(target.String()+"\n"), 0o644); err != nil {
		return fmt.Errorf("write VERSION: %w", err)
	}
	changed, err := syncProductProjections(root, target, true)
	if err != nil {
		_ = os.WriteFile(versionPath, []byte(current.String()+"\n"), 0o644)
		return err
	}
	fmt.Printf("bumped product version %s -> %s: VERSION, %s\n", current, target, strings.Join(changed, ", "))
	return nil
}

func nextProductVersion(current productVersion, value string) (productVersion, error) {
	target := current
	switch value {
	case "alpha":
		if target.Alpha == 0 {
			return productVersion{}, errors.New("alpha increment requires an existing alpha version")
		}
		target.Alpha++
	case "patch":
		target.Patch++
		target.Alpha = 0
	case "minor":
		target.Minor++
		target.Patch = 0
		target.Alpha = 0
	case "major":
		target.Major++
		target.Minor = 0
		target.Patch = 0
		target.Alpha = 0
	default:
		parsed, err := parseProductVersion(value)
		if err != nil {
			return productVersion{}, err
		}
		target = parsed
	}
	return target, nil
}

func projectWailsConfig(raw []byte, version productVersion) ([]byte, error) {
	return replaceExactlyOne(raw, regexp.MustCompile(`(?m)^  version: "[^"\r\n]+"$`), []byte(`  version: "`+version.String()+`"`))
}

type windowsInfo struct {
	Fixed struct {
		FileVersion    string `json:"file_version"`
		ProductVersion string `json:"product_version"`
	} `json:"fixed"`
	Info map[string]map[string]string `json:"info"`
}

func projectWindowsInfo(raw []byte, version productVersion) ([]byte, error) {
	var document windowsInfo
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	if len(document.Info) == 0 {
		return nil, errors.New("windows info has no language blocks")
	}
	document.Fixed.FileVersion = version.WindowsManifest()
	document.Fixed.ProductVersion = version.WindowsManifest()
	for _, values := range document.Info {
		values["FileVersion"] = version.String()
		values["ProductVersion"] = version.String()
	}
	projected, err := json.MarshalIndent(document, "", "\t")
	if err != nil {
		return nil, err
	}
	return append(projected, '\n'), nil
}

func projectNSIS(raw []byte, version productVersion) ([]byte, error) {
	pattern := regexp.MustCompile(`(?m)^    !define INFO_PRODUCTVERSION "[^"\r\n]+"$`)
	return replaceExactlyOne(raw, pattern, []byte(`    !define INFO_PRODUCTVERSION "`+version.String()+`"`))
}

func projectWindowsManifest(raw []byte, version productVersion) ([]byte, error) {
	pattern := regexp.MustCompile(`(<assemblyIdentity type="win32" name="com\.yottaapp\.yotta" version=")[^"]+(" processorArchitecture="\*"/>)`)
	match := pattern.FindSubmatchIndex(raw)
	if match == nil || pattern.FindAllIndex(raw, -1) == nil || len(pattern.FindAllIndex(raw, -1)) != 1 {
		return nil, errors.New("expected exactly one Yotta assemblyIdentity")
	}
	replacement := []byte(`${1}` + version.WindowsManifest() + `${2}`)
	return pattern.ReplaceAll(raw, replacement), nil
}

func replaceExactlyOne(raw []byte, pattern *regexp.Regexp, replacement []byte) ([]byte, error) {
	if len(pattern.FindAllIndex(raw, -1)) != 1 {
		return nil, fmt.Errorf("expected exactly one match for %s", pattern)
	}
	return pattern.ReplaceAll(raw, replacement), nil
}

func printInventory(product productVersion) {
	fmt.Printf("%-38s %-20s %s\n", "DOMAIN", "VERSION", "CLASS")
	for _, row := range inventoryRows(product) {
		fmt.Printf("%-38s %-20s %s\n", row.name, row.version, row.class)
	}
}

func inventoryRows(product productVersion) []inventoryRow {
	return []inventoryRow{
		versionRow("product", product.String(), "release"),
		versionRow(releasecompat.BuiltinNodeReleaseFormat, strconv.Itoa(releasecompat.BuiltinNodeReleaseVersion), "release-floor"),
		versionRow(releasecompat.BuiltinCatalogReleaseFormat, strconv.Itoa(releasecompat.BuiltinCatalogReleaseVersion), "release-floor"),
		versionRow(releasecompat.VersionDomainReleaseFormat, strconv.Itoa(releasecompat.VersionDomainReleaseVersion), "release-floor"),
		versionRow(storage.RootFormat, storage.LayoutVersion, "durable-layout"),
		versionRow("content-catalog-schema", strconv.Itoa(catalog.ContentSchemaVersion), "durable-schema", "8", strconv.Itoa(catalog.ContentSchemaVersion)),
		versionRow("run-ledger-schema", strconv.Itoa(catalog.RunSchemaVersion), "durable-schema"),
		versionRow(catalog.BackupFormat, strconv.Itoa(catalog.BackupVersion), "backup"),
		versionRow(storagemigrate.PlanFormat, strconv.Itoa(storagemigrate.DocumentVersion), "migration-state"),
		versionRow(storagemigrate.JournalFormat, strconv.Itoa(storagemigrate.DocumentVersion), "migration-state"),
		versionRow(storagemigrate.DiagnosticsFormat, strconv.Itoa(storagemigrate.DocumentVersion), "migration-state"),
		versionRow(storagemigrate.SnapshotFormat, strconv.Itoa(storagemigrate.SnapshotVersion), "migration-state"),
		versionRow(services.SettingsFormat, services.SettingsSchemaVersion, "user-data"),
		versionRow(schema.Format, schema.Version, "user-data", "1", "2", "3", "4", schema.Version),
		versionRow(workflowbundle.Format, strconv.Itoa(workflowbundle.Version), "portable", "1", "2", "3"),
		versionRow(datatype.Format, datatype.Version, "contract"),
		versionRow(datatype.ValueEnvelopeFormat, datatype.ValueEnvelopeVersion, "durable-value"),
		versionRow(nodecontract.Format, nodecontract.Version, "contract", "1", "3", nodecontract.Version),
		versionRow(nodeauthoring.Format, nodeauthoring.Version, "derived"),
		versionRow(nodecatalog.Format, nodecatalog.Version, "contract"),
		versionRow("builtin-node-generator", nodes.GeneratorVersion, "generator"),
		versionRow(compiler.ProgramFormat, compiler.ProgramVersion, "derived"),
		versionRow(capability.DefinitionFormat, capability.DefinitionVersion, "contract"),
		versionRow(capability.PlanFormat, capability.PlanVersion, "run-artifact"),
		versionRow(capability.RunGrantFormat, capability.RunGrantVersion, "run-artifact"),
		versionRow(runartifact.RecordFormat, runartifact.RecordVersion, "run-artifact"),
		versionRow(runartifact.LedgerSummaryFormat, runartifact.LedgerSummaryVersion, "user-data"),
		versionRow("run-store-layout", runartifact.LayoutVersion, "durable-layout"),
		versionRow("asset-record-schema", strconv.Itoa(asset.RecordSchemaVersion), "user-data"),
		versionRow("schedule-schema", string(schedule.CurrentSchemaVersion), "user-data"),
		versionRow("snippet-schema", snippet.SchemaVersion, "user-data"),
		versionRow("macro-schema", strconv.Itoa(macro.SchemaVersion), "user-data"),
		versionRow(inputclip.MediaType, strconv.FormatUint(uint64(inputclip.FormatVersion), 10), "user-data"),
		versionRow(nodepackage.Format, nodepackage.Version, "portable"),
		versionRow(nodepackage.TrustPolicyFormat, nodepackage.TrustPolicyVersion, "user-data"),
		versionRow(nodepackage.SignatureFormat, nodepackage.SignatureVersion, "portable"),
		versionRow(nodepackage.RegistryFormat, nodepackage.RegistryVersion, "user-data"),
		versionRow(installedautomation.InstallationManifestFormat, strconv.Itoa(installedautomation.InstallationManifestVersion), "contract"),
		versionRow("automation-target-profile", installedautomation.ProfileVersionV1, "user-data"),
		versionRow("host-interface", hostapi.Current, "protocol"),
		versionRow("script-worker-protocol", scriptengine.Protocol, "protocol"),
		versionRow("plugin-protocol", pluginprotocol.Protocol, "protocol"),
		versionRow(mcpserver.CatalogSearchFormat, mcpserver.CatalogSearchVersion, "protocol"),
		versionRow(mcpserver.CatalogDescriptionFormat, mcpserver.CatalogDescriptionVersion, "protocol"),
		versionRow("blob-store-layout", blob.LayoutVersion, "durable-layout"),
		versionRow("workflow-source-store-layout", workflowstore.SourceLayoutVersion, "durable-layout"),
		versionRow("program-store-layout", workflowstore.ProgramLayoutVersion, "derived-layout"),
	}
}

func versionRow(name, version, class string, readableVersions ...string) inventoryRow {
	if len(readableVersions) == 0 {
		readableVersions = []string{version}
	}
	return inventoryRow{
		name: name, version: version, class: class,
		readableVersions: append([]string(nil), readableVersions...),
	}
}

func runVersionDomainCompatibility(root string, product productVersion, arguments []string) error {
	flags := flag.NewFlagSet("yotta-versions compatibility", flag.ContinueOnError)
	write := flags.Bool("write", false, "freeze the current product release before checking")
	requireCurrent := flags.Bool("require-current", false, "require a snapshot for the current product version")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: yotta-versions compatibility [--write] [--require-current]")
	}
	domains, err := currentVersionDomains(product)
	if err != nil {
		return err
	}
	releases := releasecompat.VersionDomainReleases{Root: filepath.Join(root, "contracts", "releases")}
	if *write {
		if err := releases.Freeze(product.String(), domains); err != nil {
			return err
		}
	}
	checked, err := releases.Check(product.String(), domains, *requireCurrent || *write)
	if err != nil {
		return err
	}
	fmt.Printf("version-domain compatibility OK: %d release floor(s), %d tracked domains\n", checked, len(domains))
	return nil
}

func currentVersionDomains(product productVersion) ([]releasecompat.CurrentVersionDomain, error) {
	rows := inventoryRows(product)
	result := make([]releasecompat.CurrentVersionDomain, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, duplicate := seen[row.name]; duplicate {
			return nil, fmt.Errorf("version inventory domain %q is duplicated", row.name)
		}
		seen[row.name] = struct{}{}
		if !releaseTrackedClass(row.class) {
			continue
		}
		result = append(result, releasecompat.CurrentVersionDomain{
			Name: row.name, CurrentVersion: row.version, Class: row.class,
			ReadableVersions: append([]string(nil), row.readableVersions...),
		})
		if row.name == runartifact.RecordFormat || row.name == runartifact.LedgerSummaryFormat {
			result[len(result)-1].MigratableVersions = []string{"1"}
			result[len(result)-1].MigrationCommand = "go run ./cmd/yotta-runtime-migrate --profile <root> --write"
		}
		if row.name == nodecontract.Format {
			result[len(result)-1].MigratableVersions = []string{"2"}
			result[len(result)-1].MigrationCommand = "go run ./cmd/yotta-runtime-migrate --node-contract <file> --write"
		}
	}
	return result, nil
}

func releaseTrackedClass(class string) bool {
	switch class {
	case "release", "release-floor", "derived", "generator", "derived-layout":
		return false
	default:
		return true
	}
}

func checkStoragePathOwnership(root string) error {
	scanRoots := []string{"internal", "pkg", "cmd"}
	banned := []struct {
		pattern *regexp.Regexp
		label   string
	}{
		{regexp.MustCompile(`filepath\.Join\(\s*filepath\.Dir\([^)]+\),\s*"data"\s*\)`), "exe-relative data root"},
		{regexp.MustCompile(`(?:New|Open)ConfiguredApp\(\s*""\s*,`), "implicit settings root"},
		{regexp.MustCompile(`DevOutputPath\(\s*"captures"\s*\)`), "development capture root"},
		{regexp.MustCompile(`YOTTA_DATA_DIR`), "split data-root environment variable"},
		{regexp.MustCompile(`FileDir:\s*"logs"`), "relative log root"},
	}
	var violations []string
	for _, relativeRoot := range scanRoots {
		absoluteRoot := filepath.Join(root, relativeRoot)
		err := filepath.WalkDir(absoluteRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if filepath.Clean(relative) == filepath.Clean(filepath.Join("cmd", "yotta-versions", "main.go")) {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, rule := range banned {
				if rule.pattern.Match(raw) {
					violations = append(violations, filepath.ToSlash(relative)+" ("+rule.label+")")
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("scan storage path ownership in %s: %w", relativeRoot, err)
		}
	}
	if len(violations) != 0 {
		return fmt.Errorf("production storage paths must come from storage.Roots: %s", strings.Join(violations, ", "))
	}
	return nil
}
