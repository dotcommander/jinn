package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/toolschema"
	"github.com/tiktoken-go/tokenizer"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const (
	mcpExplorerCostSchemaVersion = 1
	mcpExplorerDefaultEncoding   = "o200k_base"
)

type mcpExplorerCostFormat struct {
	Bytes  int `json:"bytes"`
	Runes  int `json:"runes"`
	Tokens int `json:"tokens"`
}

type mcpExplorerCostTool struct {
	Name            string `json:"name"`
	CanonicalTokens int    `json:"canonical_tokens"`
	SignatureTokens int    `json:"signature_tokens"`
	SavedTokens     int    `json:"saved_tokens"`
}

type mcpExplorerCostOutput struct {
	SchemaVersion int    `json:"schema_version"`
	Encoding      string `json:"encoding"`
	ToolCount     int    `json:"tool_count"`
	Formats       struct {
		CanonicalList mcpExplorerCostFormat `json:"canonical_list"`
		SignatureList mcpExplorerCostFormat `json:"signature_list"`
	} `json:"formats"`
	Savings struct {
		Tokens  int     `json:"tokens"`
		Percent float64 `json:"percent"`
	} `json:"savings"`
	LargestTools []mcpExplorerCostTool `json:"largest_tools"`
}

func runMCPExplorerCost(ctx context.Context, c *mcpexplore.Client, encodingName, format string) error {
	discovery, tools, err := discoverMCPExplorerTools(ctx, c)
	if err != nil {
		return err
	}
	output, err := newMCPExplorerCostOutput(discovery, tools, encodingName)
	if err != nil {
		return err
	}
	return writeMCPExplorerOutput(os.Stdout, format, output)
}

func newMCPExplorerCostOutput(discovery *protocol.DiscoverResult, tools []*protocol.Tool, encodingName string) (mcpExplorerCostOutput, error) {
	encodingName, codec, err := mcpExplorerTokenizer(encodingName)
	if err != nil {
		return mcpExplorerCostOutput{}, err
	}
	canonical := mcpExplorerListOutput{Server: discovery.Meta.ServerInfo, Discovery: discovery, Tools: tools}
	signatures := newMCPExplorerSignaturesOutput(discovery.Meta.ServerInfo, tools)
	canonicalBytes, err := renderMCPExplorerJSON(canonical)
	if err != nil {
		return mcpExplorerCostOutput{}, err
	}
	signatureBytes, err := renderMCPExplorerJSON(signatures)
	if err != nil {
		return mcpExplorerCostOutput{}, err
	}
	canonicalFormat, err := mcpExplorerFormatCost(codec, canonicalBytes)
	if err != nil {
		return mcpExplorerCostOutput{}, err
	}
	signatureFormat, err := mcpExplorerFormatCost(codec, signatureBytes)
	if err != nil {
		return mcpExplorerCostOutput{}, err
	}

	output := mcpExplorerCostOutput{SchemaVersion: mcpExplorerCostSchemaVersion, Encoding: encodingName, ToolCount: len(tools)}
	output.Formats.CanonicalList = canonicalFormat
	output.Formats.SignatureList = signatureFormat
	output.Savings.Tokens = canonicalFormat.Tokens - signatureFormat.Tokens
	if canonicalFormat.Tokens > 0 {
		output.Savings.Percent = float64(output.Savings.Tokens) * 100 / float64(canonicalFormat.Tokens)
	}
	for _, tool := range tools {
		canonicalTool, err := renderMCPExplorerJSON(tool)
		if err != nil {
			return mcpExplorerCostOutput{}, err
		}
		signatureTool, err := renderMCPExplorerJSON(mcpExplorerSignatureTool{Name: tool.Name, Signature: toolschema.Render(tool.Name, tool.InputSchema), Description: tool.Description})
		if err != nil {
			return mcpExplorerCostOutput{}, err
		}
		canonicalToolCost, err := mcpExplorerFormatCost(codec, canonicalTool)
		if err != nil {
			return mcpExplorerCostOutput{}, err
		}
		signatureToolCost, err := mcpExplorerFormatCost(codec, signatureTool)
		if err != nil {
			return mcpExplorerCostOutput{}, err
		}
		output.LargestTools = append(output.LargestTools, mcpExplorerCostTool{
			Name: tool.Name, CanonicalTokens: canonicalToolCost.Tokens, SignatureTokens: signatureToolCost.Tokens,
			SavedTokens: canonicalToolCost.Tokens - signatureToolCost.Tokens,
		})
	}
	sort.Slice(output.LargestTools, func(i, j int) bool {
		if output.LargestTools[i].SavedTokens == output.LargestTools[j].SavedTokens {
			return output.LargestTools[i].Name < output.LargestTools[j].Name
		}
		return output.LargestTools[i].SavedTokens > output.LargestTools[j].SavedTokens
	})
	if len(output.LargestTools) > 10 {
		output.LargestTools = output.LargestTools[:10]
	}
	return output, nil
}

func mcpExplorerTokenizer(name string) (string, tokenizer.Codec, error) {
	if name == "" {
		name = mcpExplorerDefaultEncoding
	}
	var encoding tokenizer.Encoding
	switch name {
	case mcpExplorerDefaultEncoding:
		encoding = tokenizer.O200kBase
	case "cl100k_base":
		encoding = tokenizer.Cl100kBase
	default:
		return "", nil, fmt.Errorf("invalid --encoding %q: use o200k_base or cl100k_base", name)
	}
	codec, err := tokenizer.Get(encoding)
	if err != nil {
		return "", nil, fmt.Errorf("load tokenizer %q: %w", name, err)
	}
	return name, codec, nil
}

func renderMCPExplorerJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func mcpExplorerFormatCost(codec tokenizer.Codec, rendered []byte) (mcpExplorerCostFormat, error) {
	ids, _, err := codec.Encode(string(rendered))
	if err != nil {
		return mcpExplorerCostFormat{}, err
	}
	return mcpExplorerCostFormat{Bytes: len(rendered), Runes: utf8.RuneCount(rendered), Tokens: len(ids)}, nil
}
