package switcher

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/danielfoehrkn/kubectlSwitch/pkg"
	"github.com/danielfoehrkn/kubectlSwitch/pkg/config"
	"github.com/danielfoehrkn/kubectlSwitch/pkg/store"
	"github.com/danielfoehrkn/kubectlSwitch/pkg/subcommands/clean"
	"github.com/danielfoehrkn/kubectlSwitch/pkg/subcommands/hooks"
	"github.com/danielfoehrkn/kubectlSwitch/types"
	vaultapi "github.com/hashicorp/vault/api"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const vaultTokenFileName = ".vault-token"

var (
	// root command
	kubeconfigPath string
	kubeconfigName string
	showPreview    bool

	// vault store
	storageBackend          string
	vaultAPIAddressFromFlag string

	// hook command
	configPath     string
	stateDirectory string
	hookName       string
	runImmediately bool

	rootCommand = &cobra.Command{
		Use:   "switch",
		Short: "Launch the kubeconfig switcher",
		Long:  `The kubectx for operators.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			switchConfig, err := config.LoadConfigFromFile(configPath)
			if err != nil {
				return fmt.Errorf("failed to read switch config file: %v", err)
			}

			if switchConfig == nil {
				switchConfig = &types.Config{}
			}

			if len(kubeconfigPath) > 0 {
				switchConfig.KubeconfigPaths = append(switchConfig.KubeconfigPaths, types.KubeconfigPath{
					Path:  kubeconfigPath,
					Store: types.StoreKind(storageBackend),
				})
			}

			var (
				useVaultStore      = false
				useFilesystemStore = false
				stores             []store.KubeconfigStore
			)

			for _, configuredKubeconfigPath := range switchConfig.KubeconfigPaths {
				var s store.KubeconfigStore

				switch configuredKubeconfigPath.Store {
				case types.StoreKindFilesystem:
					if useFilesystemStore {
						continue
					}
					useFilesystemStore = true
					s = &store.FilesystemStore{
						Logger:          logrus.New().WithField("store", types.StoreKindFilesystem),
						KubeconfigPaths: switchConfig.KubeconfigPaths,
						KubeconfigName:  kubeconfigName,
					}
				case types.StoreKindVault:
					if useVaultStore {
						continue
					}
					useVaultStore = true
					vaultStore, err := getVaultStore(switchConfig.VaultAPIAddress, switchConfig.KubeconfigPaths)
					if err != nil {
						return err
					}
					s = vaultStore
				default:
					return fmt.Errorf("unknown store %q", configuredKubeconfigPath.Store)
				}

				stores = append(stores, s)
			}

			return pkg.Switcher(stores, switchConfig, configPath, stateDirectory, showPreview)
		},
	}
)

func getVaultStore(vaultAPIAddressFromSwitchConfig string, paths []types.KubeconfigPath) (*store.VaultStore, error) {
	vaultAPI := vaultAPIAddressFromSwitchConfig

	if len(vaultAPIAddressFromFlag) > 0 {
		vaultAPI = vaultAPIAddressFromFlag
	}

	vaultAddress := os.Getenv("VAULT_ADDR")
	if len(vaultAddress) > 0 {
		vaultAPI = vaultAddress
	}

	if len(vaultAPI) == 0 {
		return nil, fmt.Errorf("when using the vault kubeconfig store, the API address of the vault has to be provided either by command line argument \"vaultAPI\", via environment variable \"VAULT_ADDR\" or via SwitchConfig file")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	var vaultToken string

	// https://www.vaultproject.io/docs/commands/token-helper
	tokenBytes, _ := ioutil.ReadFile(fmt.Sprintf("%s/%s", home, vaultTokenFileName))
	if tokenBytes != nil {
		vaultToken = string(tokenBytes)
	}

	vaultTokenEnv := os.Getenv("VAULT_TOKEN")
	if len(vaultTokenEnv) > 0 {
		vaultToken = vaultTokenEnv
	}

	if len(vaultToken) == 0 {
		return nil, fmt.Errorf("when using the vault kubeconfig store, a vault API token must be provided.  Per default, the token file in  \"~.vault-token\" is used. The default token can be overriden via the  environment variable \"VAULT_TOKEN\"")
	}

	config := &vaultapi.Config{
		Address: vaultAPI,
	}
	client, err := vaultapi.NewClient(config)
	if err != nil {
		return nil, err
	}
	client.SetToken(vaultToken)

	return &store.VaultStore{
		Logger:          logrus.New().WithField("store", types.StoreKindVault),
		KubeconfigName:  kubeconfigName,
		KubeconfigPaths: paths,
		Client:          client,
	}, nil
}

func init() {
	deleteCmd := &cobra.Command{
		Use:   "clean",
		Short: "Cleans all temporary kubeconfig files",
		Long:  `Cleans the temporary kubeconfig files created in the directory $HOME/.kube/switch_tmp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return clean.Clean()
		},
	}

	hookCmd := &cobra.Command{
		Use:   "hooks",
		Short: "Run configured hooks",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logrus.New().WithField("hook", hookName)
			return hooks.Hooks(log, configPath, stateDirectory, hookName, runImmediately)
		},
	}

	hookCmd.Flags().StringVar(
		&configPath,
		"config-path",
		os.ExpandEnv("$HOME/.kube/switch-config.yaml"),
		"path on the local filesystem to the configuration file.")

	hookCmd.Flags().StringVar(
		&stateDirectory,
		"state-directory",
		os.ExpandEnv("$HOME/.kube/switch-state"),
		"path to the state directory.")

	hookCmd.Flags().StringVar(
		&hookName,
		"hook-name",
		"",
		"the name of the hook that should be run.")

	hookCmd.Flags().BoolVar(
		&runImmediately,
		"run-immediately",
		true,
		"run hooks right away. Do not respect the hooks execution configuration.")

	rootCommand.AddCommand(deleteCmd)
	rootCommand.AddCommand(hookCmd)
}

func NewCommandStartSwitcher() *cobra.Command {
	return rootCommand
}

func init() {
	rootCommand.Flags().StringVar(
		&kubeconfigPath,
		"kubeconfig-path",
		os.ExpandEnv("$HOME/.kube/config"),
		"path to be recursively searched for kubeconfig files.  Can be a file or a directory on the local filesystem or a path in Vault.")
	rootCommand.Flags().StringVar(
		&storageBackend,
		"store",
		"filesystem",
		"the backing store to be searched for kubeconfig files. Can be either \"filesystem\" or \"vault\"")
	rootCommand.Flags().StringVar(
		&kubeconfigName,
		"kubeconfig-name",
		"config",
		"only shows kubeconfig files with this name. Accepts wilcard arguments '*' and '?'. Defaults to 'config'.")
	rootCommand.Flags().BoolVar(
		&showPreview,
		"show-preview",
		true,
		"show preview of the selected kubeconfig. Possibly makes sense to disable when using vault as the kubeconfig store to prevent excessive requests against the API.")
	rootCommand.Flags().StringVar(
		&vaultAPIAddressFromFlag,
		"vault-api-address",
		"",
		"the API address of the Vault store. Overrides the default \"vaultAPIAddress\" field in the SwitchConfig. This flag is overridden by the environment variable \"VAULT_ADDR\".")
	rootCommand.Flags().StringVar(
		&stateDirectory,
		"state-directory",
		os.ExpandEnv("$HOME/.kube/switch-state"),
		"path to the local directory used for storing internal state.")
	rootCommand.Flags().StringVar(
		&configPath,
		"config-path",
		os.ExpandEnv("$HOME/.kube/switch-config.yaml"),
		"path on the local filesystem to the configuration file.")
}
