---
layout: default
title: mesheryctl-registry-update
permalink: reference/mesheryctl/registry/update
redirect_from: reference/mesheryctl/registry/update/
type: reference
display-title: "false"
language: en
command: registry
subcommand: update
---

# mesheryctl registry update

Update the registry with latest data.

## Synopsis

Updates the component metadata (SVGs, shapes, styles and other) by referring from a Google Spreadsheet.
Documentation for components can be found at https://docs.meshery.io/reference/mesheryctl/registry/update
<pre class='codeblock-pre'>
<div class='codeblock'>
mesheryctl registry update [flags]

</div>
</pre> 

## Examples

Update models from Meshery Integration Spreadsheet
<pre class='codeblock-pre'>
<div class='codeblock'>
mesheryctl registry update --spreadsheet-id [id] --spreadsheet-cred "$CRED" -i [path to the directory containing models].

</div>
</pre> 

Updating models in the meshery/meshery repository based on the spreadsheet
<pre class='codeblock-pre'>
<div class='codeblock'>
mesheryctl registry update --spreadsheet-id 1DZHnzxYWOlJ69Oguz4LkRVTFM79kC2tuvdwizOJmeMw --spreadsheet-cred "$CRED"

</div>
</pre> 

Updating models in the meshery/meshery repository based on flag
<pre class='codeblock-pre'>
<div class='codeblock'>
mesheryctl registry update --spreadsheet-id 1DZHnzxYWOlJ69Oguz4LkRVTFM79kC2tuvdwizOJmeMw --spreadsheet-cred "$CRED" --model "[model-name]"

</div>
</pre> 

## Options

<pre class='codeblock-pre'>
<div class='codeblock'>
  -h, --help                      help for update
  -i, --input string              relative or absolute input path to the models directory (default "../server/meshmodel")
  -m, --model string              specific model name to be generated
      --spreadsheet-cred string   base64 encoded credential to download the spreadsheet
      --spreadsheet-id string     spreadsheet it for the integration spreadsheet

</div>
</pre>

## Options inherited from parent commands

<pre class='codeblock-pre'>
<div class='codeblock'>
      --config string   path to config file (default "/home/runner/.meshery/config.yaml")
  -v, --verbose         verbose output

</div>
</pre>

## See Also

Go back to [command reference index](/reference/mesheryctl/), if you want to add content manually to the CLI documentation, please refer to the [instruction](/project/contributing/contributing-cli#preserving-manually-added-documentation) for guidance.
