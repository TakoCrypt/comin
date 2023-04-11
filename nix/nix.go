package nix

import (
	"io"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	EXPECTED_MACHINE_ID_FILEPATH = "/etc/comin/expected-machine-id"
)


// GetExpectedMachineId evals
// nixosConfigurations.MACHINE.config.services.comin.machineId and
// returns (true, machine-id, nil) is comin.machineId is set, (false,
// "", nil) otherwise.
func getExpectedMachineId(path, hostname string) (isSet bool, machineId string, err error) {
	expr := fmt.Sprintf("%s#nixosConfigurations.%s.config.services.comin.machineId", path, hostname)
	args := []string{
		"eval",
		expr,
		"--json",
	}
	var stdout bytes.Buffer
	err = runNixCommand(args, &stdout, os.Stderr)
	if err != nil {
		return
	}
	var machineIdPtr *string
	err = json.Unmarshal(stdout.Bytes(), &machineIdPtr)
	if err != nil {
		return
	}
	if machineIdPtr != nil {
		logrus.Debugf("Getting comin.machineId = %s", *machineIdPtr)
		machineId = *machineIdPtr
		isSet = true
	} else {
		logrus.Debugf("Getting comin.machineId = null (not set)")
	}
	return
}

func runNixCommand(args []string, stdout, stderr io.Writer) (err error) {
	commonArgs := []string{"--extra-experimental-features", "nix-command", "--extra-experimental-features", "flakes"}
	args = append(commonArgs, args...)
	cmdStr := fmt.Sprintf("nix %s", strings.Join(args, " "))
	logrus.Infof("Running '%s'", cmdStr)
	cmd := exec.Command("nix", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("Command '%s' fails with %s", cmdStr, err)
	}
	return nil
}

func showDerivation(path, hostname string) (drvPath string, outPath string, err error) {
	installable := fmt.Sprintf("%s#nixosConfigurations.%s.config.system.build.toplevel", path, hostname)
	args := []string{
		"show-derivation",
		installable,
		"-L",
	}
	var stdout bytes.Buffer
	err = runNixCommand(args, &stdout, os.Stderr)
	if err != nil {
		return
	}

	var output map[string]Derivation
	err = json.Unmarshal(stdout.Bytes(), &output)
	if err != nil {
		return
	}
	keys := make([]string, 0, len(output))
	for key := range output {
		keys = append(keys, key)
	}
	drvPath = keys[0]
	outPath = output[drvPath].Outputs.Out.Path
	logrus.Infof("The derivation path is %s", drvPath)
	logrus.Infof("The output path is %s", outPath)
	return
}

type Path struct {
	Path string `json:"path"`
}

type Output struct {
	Out Path `json:"out"`
}

type Derivation struct {
	Outputs Output `json:"outputs"`
}

type Show struct {
	NixosConfigurations map[string]struct{} `json:"nixosConfigurations"`
}

func List() (hosts []string, err error) {
	args := []string{
		"flake",
		"show",
		"--json"}
	var stdout bytes.Buffer
	err = runNixCommand(args, &stdout, os.Stderr)
	if err != nil {
		return
	}

	var output Show
	err = json.Unmarshal(stdout.Bytes(), &output)
	if err != nil {
		return
	}
	hosts = make([]string, 0, len(output.NixosConfigurations))
	for key := range output.NixosConfigurations {
		hosts = append(hosts, key)
	}
	return
}

func Build(path, hostname string) (outPath string, err error) {
	drvPath, outPath, err := showDerivation(path, hostname)
	if err != nil {
		return
	}

	args := []string{
		"build",
		drvPath,
		"-L",
		"--no-link"}
	err = runNixCommand(args, os.Stdout, os.Stderr)
	if err != nil {
		return
	}
	return
}

// checkMachineId checks the specified machineId (via the
// comin.machineId option) is equal to the machine id of the machine
// being configured. If not, it returns an error. Note this is
// optional: if the comin.machineId option is not set, this check is
// skipped.
func checkMachineId(path, hostname string) error {
	isSet, expectedMachineId, err := getExpectedMachineId(path, hostname)
	if err != nil {
		return err
	} else if isSet {
		machineIdBytes, err := os.ReadFile("/etc/machine-id")
		machineId := strings.TrimSuffix(string(machineIdBytes), "\n")
		if err != nil {
			return fmt.Errorf("Can not read file '/etc/machine-id': %s", err)
		}
		if expectedMachineId != machineId {
			return fmt.Errorf("Skip deployment because the comin expected machine id '%s' is not equal to the actual machine id '%s'",
				expectedMachineId, machineId)
		}
	}
	return nil
}

func setSystemProfile(operation string, outPath string, dryRun bool) error {
	if operation == "switch" || operation == "boot" {
		cmdStr := fmt.Sprintf("nix-env --profile /nix/var/nix/profiles/system --set %s", outPath)
		logrus.Infof("Running '%s'", cmdStr)
		cmd := exec.Command("nix-env", "--profile", "/nix/var/nix/profiles/system", "--set", outPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if dryRun {
			logrus.Infof("Dry-run enabled: '%s' has not been executed", cmdStr)
		} else {
			err := cmd.Run()
			if err != nil {
				return fmt.Errorf("Command '%s' fails with %s", cmdStr, err)
			}
			logrus.Infof("Command '%s' succeeded", cmdStr)
		}
	}
	return nil
}

func createGcRoot(stateDir, hostname, outPath string, dryRun bool) error {
	gcRootDir := filepath.Join(stateDir, "gcroots")
	gcRoot := filepath.Join(
		gcRootDir,
		fmt.Sprintf("switch-to-configuration-%s", hostname))
	if dryRun {
		logrus.Infof("Dry-run enabled: 'ln -s %s %s'", outPath, gcRoot)
		return nil
	}
	if err := os.MkdirAll(gcRootDir, 0750); err != nil {
		return err
	}
	// TODO: only remove if file already exists
	os.Remove(gcRoot)
	if err := os.Symlink(outPath, gcRoot); err != nil {
		return fmt.Errorf("Failed to create symlink 'ln -s %s %s': %s", outPath, gcRoot, err)
	}
	logrus.Infof("Creating gcroot '%s'", gcRoot)
	return nil
}

func cominUnitFileHash() (string, error) {
	logrus.Infof("Generating the comin.service unit file sha256: 'systemctl cat comin.service | sha256sum'")
	cmd := exec.Command("systemctl", "cat", "comin.service")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("Command 'systemctl cat comin.service' fails with %s", err)
	}
	sum := sha256.Sum256(stdout.Bytes())
	hash := fmt.Sprintf("%x", sum)
	logrus.Infof("The comin.service unit file sha256 is '%s'", hash)
	return hash, nil
}

func switchToConfiguration(operation string, outPath string, dryRun bool) error {
	switchToConfigurationExe := filepath.Join(outPath, "bin", "switch-to-configuration")
	logrus.Infof("Running '%s %s'", switchToConfigurationExe, operation)
	cmd := exec.Command(switchToConfigurationExe, operation)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if dryRun {
		logrus.Infof("Dry-run enabled: '%s switch' has not been executed", switchToConfigurationExe)
	} else {
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Command %s switch fails with %s", switchToConfigurationExe, err)
		}
		logrus.Infof("Switch successfully terminated")
	}
	return nil
}

func Deploy(hostname, stateDir, path, operation string, dryRun bool) (needToRestartComin bool, err error) {
	err = os.MkdirAll(stateDir, 0750)
	if err != nil {
		return
	}

	if err = checkMachineId(path, hostname); err != nil {
		return
	}

	outPath, err := Build(path, hostname)
	if err != nil {
		return
	}

	beforeCominUnitFileHash, err := cominUnitFileHash()
	if err != nil {
		return
	}

	// This is required to write boot entries
	if err = setSystemProfile(operation, outPath, dryRun); err != nil {
		return
	}

	if err = switchToConfiguration(operation, outPath, dryRun); err != nil {
		return
	}

	afterCominUnitFileHash, err := cominUnitFileHash()
	if err != nil {
		return
	}
	if beforeCominUnitFileHash != afterCominUnitFileHash {
		needToRestartComin = true
	}

	if err = createGcRoot(stateDir, hostname, outPath, dryRun); err != nil {
		return
	}

	logrus.Infof("Deployment succeeded")

	return
}
