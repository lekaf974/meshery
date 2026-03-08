---
title: Common Subcommand Verbs
description: Guidelines for using consistent subcommand verbs across mesheryctl commands.
categories: [contributing]
---

## Introduction

A key design goal of `mesheryctl` is to provide an intuitive, consistent, and predictable user experience. One of the most effective ways to achieve this is by standardizing the verbs (subcommands) used across different resource commands. When every resource that supports listing, viewing, or creating uses the same verb with the same flags, users can apply their knowledge from one command to another without consulting documentation.

This guide documents the standard subcommand verbs used in `mesheryctl`, their intended behavior, common flags, and concrete implementation examples. Contributors adding new commands or extending existing ones should follow these conventions to ensure consistency and a high-quality user experience.

The examples in each section are drawn from `mesheryctl component view`, `mesheryctl component list`, `mesheryctl component search`, `mesheryctl environment create`, `mesheryctl connection delete`, `mesheryctl design apply`, `mesheryctl model import`, and `mesheryctl model export`.

---

## `view`

Display detailed information about a single, specific resource. The user identifies the target resource either by its name or its UUID. When multiple resources match a name, the command prompts the user to select one interactively.

### flags

- `--output-format` / `-o`: Output format for the displayed resource. Accepted values: `json`, `yaml`. Default: `yaml`.
- `--save` / `-s`: Save the displayed output to a file on disk. Default: `false`.

### implementation details

#### flag struct

```golang
type componentViewFlags struct {
    OutputFormat string `json:"output-type" validate:"required,oneof=json yaml"`
    Save         bool   `json:"save" validate:"boolean"`
}

var cmdComponentViewFlags componentViewFlags
```

#### flag declaration

```golang
func init() {
    viewComponentCmd.Flags().StringVarP(&cmdComponentViewFlags.OutputFormat, "output-format", "o", "yaml", "(optional) format to display in [json|yaml]")
    viewComponentCmd.Flags().BoolVarP(&cmdComponentViewFlags.Save, "save", "s", false, "(optional) save output as a JSON/YAML file")
}
```

#### flag validation

Validation leverages struct field tags (`validate`) processed by `mesheryctlflags.ValidateCmdFlags`. This ensures the `--output-format` value is one of the accepted options before the command runs.

```golang
PreRunE: func(cmd *cobra.Command, args []string) error {
    return mesheryctlflags.ValidateCmdFlags(cmd, &cmdComponentViewFlags)
},
```

Positional argument validation is performed separately to ensure exactly one argument is provided:

```golang
Args: func(_ *cobra.Command, args []string) error {
    if len(args) == 0 {
        return utils.ErrInvalidArgument(fmt.Errorf("[component-name | component-id] is required but not specified\n\n%s", errViewCmdMsg))
    } else if len(args) > 1 {
        return utils.ErrInvalidArgument(fmt.Errorf("too many arguments specified\n\n%s", errViewCmdMsg))
    }
    return nil
},
```

#### flag initialisation

Flag variables are bound to the struct fields in the `init` function so Cobra populates them automatically when the user runs the command:

```golang
func init() {
    viewComponentCmd.Flags().StringVarP(&cmdComponentViewFlags.OutputFormat, "output-format", "o", "yaml", "(optional) format to display in [json|yaml]")
    viewComponentCmd.Flags().BoolVarP(&cmdComponentViewFlags.Save, "save", "s", false, "(optional) save output as a JSON/YAML file")
}
```

---

## `list`

Display a paginated table of all resources of a given type registered in Meshery Server. Supports page-by-page navigation or a simple count-only mode.

### flags

- `--page` / `-p`: Page number to display (default: `1`). Useful for navigating large result sets.
- `--pagesize` / `-s`: Number of items per page (default: `10`).
- `--count` / `-c`: Print only the total number of resources instead of the full table. Default: `false`.

### implementation details

#### flag struct

```golang
type componentListFlag struct {
    Count    bool `json:"count" validate:"boolean"`
    Page     int  `json:"page" validate:"omitempty,gte=1"`
    PageSize int  `json:"page-size" validate:"omitempty,gte=1"`
}

var cmdComponentListFlag componentListFlag
```

#### flag declaration

```golang
func init() {
    listComponentCmd.Flags().IntVarP(&cmdComponentListFlag.Page, "page", "p", 1, "(optional) List next set of components with --page (default = 1)")
    listComponentCmd.Flags().IntVarP(&cmdComponentListFlag.PageSize, "pagesize", "s", 10, "(optional) List next set of components with --pagesize (default = 10)")
    listComponentCmd.Flags().BoolVarP(&cmdComponentListFlag.Count, "count", "c", false, "(optional) Display count only")
}
```

#### flag validation

```golang
PreRunE: func(cmd *cobra.Command, args []string) error {
    return mesheryctlflags.ValidateCmdFlags(cmd, &cmdComponentListFlag)
},
```

#### flag initialisation

The `DisplayDataAsync` struct is populated from the parsed flags and passed to the shared `display.ListAsyncPagination` helper, which handles fetching pages and rendering the table:

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    componentData := display.DisplayDataAsync{
        UrlPath:          componentApiPath,
        DataType:         "component",
        Header:           []string{"ID", "Name", "Model", "Version"},
        Page:             cmdComponentListFlag.Page,
        PageSize:         cmdComponentListFlag.PageSize,
        IsPage:           cmd.Flags().Changed("page"),
        DisplayCountOnly: cmdComponentListFlag.Count,
    }
    return display.ListAsyncPagination(componentData, generateComponentDataToDisplay)
},
```

---

## `search`

Find resources whose attributes match a free-text query. The search term is provided as a positional argument. Results are displayed in a table.

### flags

- `--page` / `-p`: Page number for paginated results (default: `1`).
- `--pagesize` / `-s`: Number of results per page (default: `10`).

### implementation details

#### flag struct

The `search` command typically uses inline flag retrieval rather than a dedicated struct, though a struct can be used for more complex cases:

```golang
// Example: model search uses inline flag retrieval
RunE: func(cmd *cobra.Command, args []string) error {
    page, _ := cmd.Flags().GetInt("page")
    pageSize, _ := cmd.Flags().GetInt("pagesize")
    // ...
}
```

#### flag declaration

```golang
func init() {
    searchModelCmd.Flags().IntP("page", "p", 1, "(optional) List next set of models with --page (default = 1)")
    searchModelCmd.Flags().IntP("pagesize", "s", 10, "(optional) List next set of models with --pagesize (default = 10)")
}
```

#### flag validation

Argument validation ensures the search term is always provided:

```golang
Args: func(_ *cobra.Command, args []string) error {
    if len(args) == 0 {
        return utils.ErrInvalidArgument(fmt.Errorf("[search term] isn't specified. Please enter component name to search\n\n%v", usageErrorMessage))
    }
    return nil
},
```

#### flag initialisation

The search term from `args[0]` is URL-encoded and appended to the API path, along with pagination parameters:

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    modelData := display.DisplayDataAsync{
        UrlPath:  fmt.Sprintf("%s?search=%s", modelsApiPath, url.QueryEscape(args[0])),
        DataType: "model",
        Header:   []string{"ID", "Model", "Category", "Version"},
        Page:     page,
        PageSize: pageSize,
        IsPage:   cmd.Flags().Changed("page"),
    }
    return display.ListAsyncPagination(modelData, generateModelDataToDisplay)
},
```

---

## `create`

Create a new resource in Meshery Server. All required attributes are supplied via named flags. The command validates that required flags are present and that values are in the expected format (e.g., UUID) before sending the creation request.

### flags

- `--orgID` / `-o`: UUID of the organization in which the resource will be created. **Required**.
- `--name` / `-n`: Human-readable name for the new resource. **Required**.
- `--description` / `-d`: A brief description of the resource. **Required**.

> Note: The exact flags depend on the resource being created. The flags above reflect the environment `create` command. Always include only the flags needed for the specific resource.

### implementation details

#### flag struct

```golang
type createEnvironmentFlags struct {
    orgID       string
    name        string
    description string
}

var createEnvironmentFlagsProvided createEnvironmentFlags
```

#### flag declaration

```golang
func init() {
    createEnvironmentCmd.Flags().StringVarP(&createEnvironmentFlagsProvided.orgID, "orgID", "o", "", "Organization ID")
    createEnvironmentCmd.Flags().StringVarP(&createEnvironmentFlagsProvided.name, "name", "n", "", "Name of the environment")
    createEnvironmentCmd.Flags().StringVarP(&createEnvironmentFlagsProvided.description, "description", "d", "", "Description of the environment")
}
```

#### flag validation

Required-field and format checks are performed in `PreRunE` before the main logic executes:

```golang
PreRunE: func(cmd *cobra.Command, args []string) error {
    const errMsg = "[ Organization ID | Name | Description ] aren't specified\n\n" +
        "Usage: mesheryctl environment create --orgID [orgID] --name [name] --description [description]\n" +
        "Run 'mesheryctl environment create --help' to see detailed help message"

    if createEnvironmentFlagsProvided.orgID == "" || createEnvironmentFlagsProvided.name == "" || createEnvironmentFlagsProvided.description == "" {
        return utils.ErrInvalidArgument(errors.New(errMsg))
    }
    if !utils.IsUUID(createEnvironmentFlagsProvided.orgID) {
        return utils.ErrInvalidUUID(fmt.Errorf("invalid Environment ID: %s", createEnvironmentFlagsProvided.orgID))
    }
    return nil
},
```

#### flag initialisation

The validated flag values are assembled into a payload struct and sent to the server:

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    payload := &environment.EnvironmentPayload{
        Name:        createEnvironmentFlagsProvided.name,
        Description: createEnvironmentFlagsProvided.description,
        OrgId:       createEnvironmentFlagsProvided.orgID,
    }
    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        return err
    }
    _, err = api.Add(environmentApiPath, bytes.NewBuffer(payloadBytes), nil)
    if err != nil {
        return err
    }
    utils.Log.Infof("Environment named %s created in organization id %s",
        createEnvironmentFlagsProvided.name, createEnvironmentFlagsProvided.orgID)
    return nil
},
```

---

## `delete`

Remove a resource from Meshery Server. The target resource is identified by its UUID, passed as a positional argument.

### flags

The `delete` verb typically takes no flags — the resource ID is supplied as a positional argument.

- `[resource-id]`: UUID of the resource to delete. **Required**.

> Some `delete` implementations (e.g., `mesheryctl design delete`) also accept a `--file` / `-f` flag to identify the resource by its design file path or URL.

### implementation details

#### flag struct

The `delete` command usually operates on a single positional UUID and does not require a dedicated flag struct:

```golang
// Connection delete: no struct needed — the ID comes from args[0]
```

#### flag declaration

For commands that support file-based deletion (e.g., `mesheryctl design delete`):

```golang
func init() {
    deleteCmd.Flags().StringVarP(&file, "file", "f", "", "Path to design file")
}
```

#### flag validation

UUID format validation is enforced inside `Args`:

```golang
Args: func(_ *cobra.Command, args []string) error {
    if len(args) != 1 {
        return utils.ErrInvalidArgument(fmt.Errorf("%s\n%s", errNoArgMsg, deleteUsageMsg))
    }
    if !utils.IsUUID(args[0]) {
        return utils.ErrInvalidUUID(fmt.Errorf("invalid connection ID: %q", args[0]))
    }
    return nil
},
```

#### flag initialisation

The resource ID is read directly from `args[0]` and passed to the API call:

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    _, err := api.Delete(fmt.Sprintf("%s/%s", connectionApiPath, args[0]))
    if err != nil {
        return err
    }
    utils.Log.Infof("Connection with ID: %q is deleted", args[0])
    return nil
},
```

---

## `apply`

Deploy or apply a resource configuration to the infrastructure. The configuration can be specified as a saved resource name, a local file path, or a remote URL.

### flags

- `--file` / `-f`: Path to the design file or a remote URL to apply. The flag is optional when providing a saved resource name as a positional argument.

### implementation details

#### flag struct

```golang
var (
    skipSave    bool   // skip saving a design after apply
    patternFile string // path or URL to the design file
)
```

#### flag declaration

```golang
func init() {
    applyCmd.Flags().StringVarP(&patternFile, "file", "f", "", "Path to design file")
    applyCmd.Flags().BoolVarP(&skipSave, "skip-save", "", false, "Skip saving the design")
}
```

#### flag validation

Argument length is validated to ensure either a file flag or a positional argument is provided:

```golang
Args: cobra.MinimumNArgs(0),
```

The presence of meaningful input is verified inside `RunE` by checking `len(args)` and the `--file` flag value.

#### flag initialisation

The command resolves whether the input is a name, file, or URL and dispatches accordingly:

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    // pattern name provided as positional argument
    if len(args) > 0 {
        patternName := strings.Join(args, "%20")
        // fetch and apply by name ...
    }
    // file path or URL provided via --file flag
    if patternFile != "" {
        // read file or fetch URL and apply ...
    }
    return nil
},
```

---

## `import`

Import a resource from an external source such as a local file, directory, URL, OCI artifact, or tarball. The source is specified via the `--file` flag or as a positional argument.

### flags

- `--file` / `-f`: Path to the file, directory, URL, OCI artifact, or `.tar.gz` archive to import. **Required** if no positional argument is given.

### implementation details

#### flag struct

The `import` command uses direct flag access rather than a dedicated struct:

```golang
// Flag value retrieved inline:
file, _ := cmd.Flags().GetString("file")
```

#### flag declaration

```golang
func init() {
    importModelCmd.Flags().StringVarP(&file, "file", "f", "", "URI, file path, or directory for the model to import")
}
```

#### flag validation

```golang
Args: func(cmd *cobra.Command, args []string) error {
    const errMsg = "Usage: mesheryctl model import [ file | filePath | URL ]\n" +
        "Run 'mesheryctl model import --help' to see detailed help message"
    file, _ := cmd.Flags().GetString("file")
    if file == "" && len(args) == 0 {
        return fmt.Errorf("[ file | filepath | URL ] isn't specified\n\n%v", errMsg)
    } else if len(args) > 1 {
        return fmt.Errorf("too many arguments\n\n%v", errMsg)
    }
    return nil
},
```

#### flag initialisation

The resolved path is dispatched to the appropriate importer (URL, CSV, tarball, or directory):

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    path := file
    if path == "" {
        path = args[0]
    }
    if utils.IsValidUrl(path) {
        return registerModel(nil, nil, nil, "", "urlImport", path, true)
    }
    // handle local file / directory / tarball ...
    return nil
},
```

---

## `export`

Export a registered resource to a local file in the specified output format. The resource is identified by name and the output destination is controlled by flags.

### flags

- `--output-format` / `-t`: Format of the exported file. Accepted values: `json`, `yaml`. Default: `yaml`.
- `--output-type` / `-o`: Packaging type for the export. Accepted values: `oci`, `tar`. Default: `oci`.
- `--output-location` / `-l`: Directory path where the exported file will be saved.
- `--discard-components`: Omit component definitions from the export. Default: `false`.
- `--discard-relationships`: Omit relationship definitions from the export. Default: `false`.
- `--version`: Specific version of the resource to export (semver format).

### implementation details

#### flag struct

```golang
type exportModelFlags struct {
    DiscardComponents    bool   `json:"discard-components" validate:"boolean"`
    DiscardRelationships bool   `json:"discard-relationships" validate:"boolean"`
    OutputFormat         string `json:"output-format" validate:"required,oneof=json yaml"`
    OutputLocation       string `json:"output-location" validate:"required,dirpath"`
    OutputType           string `json:"output-type" validate:"required,oneof=oci tar"`
    Page                 int    `json:"page" validate:"omitempty,min=1"`
    Version              string `json:"version" validate:"omitempty,semver"`
}

var exportModelFlagsProvided exportModelFlags
```

#### flag declaration

```golang
func init() {
    exportModelCmd.Flags().StringVarP(&exportModelFlagsProvided.OutputFormat, "output-format", "t", "yaml", "(optional) format to export in [json|yaml]")
    exportModelCmd.Flags().StringVarP(&exportModelFlagsProvided.OutputType, "output-type", "o", "oci", "(optional) type to export [oci|tar]")
    exportModelCmd.Flags().StringVarP(&exportModelFlagsProvided.OutputLocation, "output-location", "l", ".", "(optional) output location for the exported model")
    exportModelCmd.Flags().BoolVar(&exportModelFlagsProvided.DiscardComponents, "discard-components", false, "(optional) discard components from the exported model")
    exportModelCmd.Flags().BoolVar(&exportModelFlagsProvided.DiscardRelationships, "discard-relationships", false, "(optional) discard relationships from the exported model")
    exportModelCmd.Flags().StringVar(&exportModelFlagsProvided.Version, "version", "", "(optional) version of the model to export")
}
```

#### flag validation

Struct-tag-based validation is applied in `PreRunE` using the `FlagValidator` from the command context:

```golang
PreRunE: func(cmd *cobra.Command, args []string) error {
    flagValidator, ok := cmd.Context().Value(mesheryctlflags.FlagValidatorKey).(*mesheryctlflags.FlagValidator)
    if !ok || flagValidator == nil {
        return utils.ErrCommandContextMissing("flags-validator")
    }
    return flagValidator.Validate(exportModelFlagsProvided)
},
```

Argument presence is validated separately:

```golang
Args: func(_ *cobra.Command, args []string) error {
    const errMsg = "Usage: mesheryctl model export [model-name]\n" +
        "Run 'mesheryctl model export --help' to see detailed help message"
    if len(args) == 0 {
        return utils.ErrInvalidArgument(errors.New("Please provide a model name. " + errMsg))
    }
    return nil
},
```

#### flag initialisation

The model name and query parameters are assembled and sent to the export API:

```golang
RunE: func(cmd *cobra.Command, args []string) error {
    modelName := args[0]
    queryParams := url.Values{}
    queryParams.Set("name", modelName)
    queryParams.Set("output_format", exportModelFlagsProvided.OutputFormat)
    // additional query params ...
    // call API and write response to disk
    return nil
},
```

---

## Conclusion

Using a consistent set of subcommand verbs — `view`, `list`, `search`, `create`, `delete`, `apply`, `import`, and `export` — across all `mesheryctl` resource commands is a cornerstone of the tool's usability. When a user learns how to use `mesheryctl component view`, they can immediately transfer that knowledge to `mesheryctl model view`, `mesheryctl relationship view`, and any future commands that follow the same pattern.

Contributors are encouraged to:

1. **Reuse existing verbs** before inventing new ones. Check whether the action fits one of the verbs documented here.
2. **Mirror flags** — when a verb is added to a new resource, use the same flag names, shorthand letters, and default values as the examples in this guide.
3. **Apply struct-based flag validation** using field tags and `mesheryctlflags.ValidateCmdFlags` (or `FlagValidator`) rather than writing ad-hoc validation logic.
4. **Validate arguments in `Args`** — keep `PreRunE` for flag validation and `Args` for positional argument checks so Cobra can surface helpful error messages early.
5. **Write usage examples** in the `Example` field of every `cobra.Command` covering the most common invocations.

Following these conventions keeps `mesheryctl` predictable, learnable, and maintainable as the command set continues to grow. For broader design decisions and the command design process, refer to the [Meshery CLI Style Guide](/project/contributing/contributing-cli-guide).
