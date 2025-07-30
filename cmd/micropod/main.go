package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"micropod/pkg/manager"
)

var rootCmd = &cobra.Command{
	Use:   "micropod",
	Short: "A secure container engine based on Firecracker",
	Long:  `MicroPod is a command line tool that runs OCI container images in Firecracker microVMs for enhanced security isolation.`,
}

var portMappings []string

var runCmd = &cobra.Command{
	Use:   "run [image]",
	Short: "Run a container image in a Firecracker microVM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageName := args[0]
		
		mgr := manager.NewManager()
		vmID, err := mgr.RunVM(imageName, portMappings)
		if err != nil {
			return fmt.Errorf("failed to run VM: %w", err)
		}
		
		fmt.Printf("VM started successfully with ID: %s\n", vmID)
		return nil
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List running VMs managed by micropod",
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr := manager.NewManager()
		vms, err := mgr.ListVMs()
		if err != nil {
			return fmt.Errorf("failed to list VMs: %w", err)
		}
		
		if len(vms) == 0 {
			fmt.Println("No running VMs found")
			return nil
		}
		
		fmt.Printf("%-36s %-20s %-10s %-10s %s\n", "VM ID", "IMAGE", "STATE", "PID", "CREATED")
		fmt.Println("------------------------------------------------------------------------------------")
		for _, vm := range vms {
			fmt.Printf("%-36s %-20s %-10s %-10d %s\n", 
				vm.ID, vm.ImageName, vm.State, vm.FirecrackerPid, vm.CreatedAt.Format("2006-01-02 15:04:05"))
		}
		
		return nil
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop [vm-id]",
	Short: "Stop and clean up a running VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]
		
		mgr := manager.NewManager()
		err := mgr.StopVM(vmID)
		if err != nil {
			return fmt.Errorf("failed to stop VM: %w", err)
		}
		
		fmt.Printf("VM %s stopped successfully\n", vmID)
		return nil
	},
}

var logsCmd = &cobra.Command{
	Use:   "logs [vm-id]",
	Short: "Fetch the logs of a VM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		vmID := args[0]

		mgr := manager.NewManager()
		
		// Get VM from state store to find log file path
		vms, err := mgr.ListVMs()
		if err != nil {
			return fmt.Errorf("failed to list VMs: %w", err)
		}

		var logFilePath string
		found := false
		for _, vm := range vms {
			if vm.ID == vmID {
				logFilePath = vm.LogFilePath
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("VM %s not found", vmID)
		}

		// Open log file
		logFile, err := os.Open(logFilePath)
		if err != nil {
			return fmt.Errorf("could not open log file: %w", err)
		}
		defer logFile.Close()

		// Read and follow log file (like tail -f)
		r := bufio.NewReader(logFile)
		for {
			line, err := r.ReadString('\n')
			if len(line) > 0 {
				fmt.Print(line)
			}
			if err == io.EOF {
				// Wait a bit and try again for new content
				time.Sleep(500 * time.Millisecond)
			} else if err != nil {
				return err
			}
		}
	},
}

func init() {
	runCmd.Flags().StringSliceVarP(&portMappings, "publish", "p", []string{}, "Publish a VM's port(s) to the host (e.g., 8080:80)")
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(logsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}