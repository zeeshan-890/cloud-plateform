package cmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var storageProjectID string

var storageCmd = &cobra.Command{
	Use:   "storage",
	Short: "Manage project object storage",
}

var storageBucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Ensure / show the project bucket",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if storageProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		b, mode, err := client.GetStorageBucket(cfg.CurrentOrgID, storageProjectID)
		if err != nil {
			return err
		}
		fmt.Printf("Bucket: %s  mode=%s  id=%s\n", b.Name, mode, b.ID)
		return nil
	},
}

var storageListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List objects",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if storageProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		list, err := client.ListStorageObjects(cfg.CurrentOrgID, storageProjectID, "")
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No objects.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "KEY\tSIZE\tTYPE")
		for _, o := range list {
			fmt.Fprintf(w, "%s\t%d\t%s\n", o.Key, o.SizeBytes, o.ContentType)
		}
		return w.Flush()
	},
}

var storagePutCmd = &cobra.Command{
	Use:   "put <key> <text>",
	Short: "Upload a small text object (base64 over API)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if storageProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		b64 := base64.StdEncoding.EncodeToString([]byte(args[1]))
		o, err := client.UploadStorageObject(cfg.CurrentOrgID, storageProjectID, args[0], b64, "text/plain")
		if err != nil {
			return err
		}
		fmt.Printf("Uploaded %s (%d bytes)\n", o.Key, o.SizeBytes)
		return nil
	},
}

var storageSignCmd = &cobra.Command{
	Use:   "sign <key>",
	Short: "Print a signed GET URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if storageProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		url, err := client.SignedStorageURL(cfg.CurrentOrgID, storageProjectID, args[0], "15m")
		if err != nil {
			return err
		}
		fmt.Println(url)
		return nil
	},
}

var storageRmCmd = &cobra.Command{
	Use:   "rm <key>",
	Short: "Delete an object",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, err := newClient()
		if err != nil {
			return err
		}
		if err := requireLogin(cfg); err != nil {
			return err
		}
		if cfg.CurrentOrgID == "" {
			return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
		}
		if storageProjectID == "" {
			return fmt.Errorf("--project is required")
		}
		if err := client.DeleteStorageObject(cfg.CurrentOrgID, storageProjectID, args[0]); err != nil {
			return err
		}
		fmt.Println("Deleted.")
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{storageBucketCmd, storageListCmd, storagePutCmd, storageSignCmd, storageRmCmd} {
		c.Flags().StringVar(&storageProjectID, "project", "", "Project ID")
	}
	storageCmd.AddCommand(storageBucketCmd, storageListCmd, storagePutCmd, storageSignCmd, storageRmCmd)
	rootCmd.AddCommand(storageCmd)
}
