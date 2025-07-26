package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"micropod/pkg/manager"
)

var rootCmd = &cobra.Command{
	Use:   "micropod",
	Short: "A secure container engine based on Firecracker",
	Long:  `MicroPod is a command line tool that runs OCI container images in Firecracker microVMs for enhanced security isolation.`,
}

var runCmd = &cobra.Command{
	Use:   "run [image]",
	Short: "Run a container image in a Firecracker microVM",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		imageName := args[0]
		
		mgr := manager.NewManager()
		vmID, err := mgr.RunVM(imageName)
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

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(stopCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}