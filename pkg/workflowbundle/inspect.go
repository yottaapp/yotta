// Package workflowbundle exposes the stable, read-only Workflow Bundle contract.
package workflowbundle

import (
	"context"
	"fmt"
	"sort"

	internalbundle "github.com/yottaapp/yotta/internal/workflowbundle"
)

const (
	Format    = internalbundle.Format
	Version   = internalbundle.Version
	Extension = internalbundle.Extension
)

type NodeRef struct {
	NodeTypeID     string `json:"nodeTypeId"`
	Version        string `json:"version"`
	SemanticDigest string `json:"semanticDigest"`
}

type NodePackDependency struct {
	PublisherNamespace string    `json:"publisherNamespace"`
	PackageID          string    `json:"packageId"`
	PackageVersion     string    `json:"packageVersion"`
	ManifestDigest     string    `json:"manifestDigest"`
	NodeRefs           []NodeRef `json:"nodeRefs"`
}

type Info struct {
	PublishedSourceHash        string               `json:"publishedSourceHash"`
	PanelCount                 int                  `json:"panelCount"`
	WorkflowID                 string               `json:"workflowId"`
	Name                       string               `json:"name"`
	Revision                   int64                `json:"revision"`
	SourceHash                 string               `json:"sourceHash"`
	ResourceCount              int                  `json:"resourceCount"`
	TargetProfileCount         int                  `json:"targetProfileCount"`
	CredentialRequirementCount int                  `json:"credentialRequirementCount"`
	Dependencies               []NodePackDependency `json:"dependencies"`
	BlobCount                  int                  `json:"blobCount"`
	BlobBytes                  int64                `json:"blobBytes"`
	SourceTrust                string               `json:"sourceTrust"`
}

// Inspect validates the complete archive and returns only portable contract facts.
// It never upgrades source trust or interprets attached publication evidence.
func Inspect(ctx context.Context, raw []byte) (Info, error) {
	internalInfo, manifest, err := internalbundle.InspectBytes(ctx, raw)
	if err != nil {
		return Info{}, err
	}
	dependencies := make([]NodePackDependency, 0, len(manifest.Dependencies))
	for _, dependency := range manifest.Dependencies {
		nodeRefs := make([]NodeRef, 0, len(dependency.NodeRefs))
		for _, ref := range dependency.NodeRefs {
			nodeRefs = append(nodeRefs, NodeRef{
				NodeTypeID: ref.NodeTypeID, Version: ref.Version, SemanticDigest: string(ref.SemanticDigest),
			})
		}
		dependencies = append(dependencies, NodePackDependency{
			PublisherNamespace: dependency.PublisherNamespace,
			PackageID:          dependency.PackageID, PackageVersion: dependency.PackageVersion,
			ManifestDigest: string(dependency.ManifestDigest), NodeRefs: nodeRefs,
		})
	}
	for _, resource := range manifest.Panels {
		if resource.Plugin == nil {
			continue
		}
		p := resource.Plugin
		found := false
		for _, d := range dependencies {
			if d.PackageID == p.PackageID {
				found = true
				if d.ManifestDigest != p.ManifestDigest {
					return Info{}, fmt.Errorf("conflicting panel package dependency")
				}
			}
		}
		if !found {
			dependencies = append(dependencies, NodePackDependency{PublisherNamespace: p.PublisherNamespace, PackageID: p.PackageID, PackageVersion: p.PackageVersion, ManifestDigest: p.ManifestDigest, NodeRefs: []NodeRef{}})
		}
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].PackageID < dependencies[j].PackageID })
	return Info{
		PanelCount: len(manifest.Panels),
		WorkflowID: internalInfo.WorkflowID, Name: internalInfo.Name, Revision: internalInfo.Revision,
		PublishedSourceHash: string(internalInfo.PublishedSourceHash), SourceHash: string(internalInfo.SourceHash), ResourceCount: internalInfo.ResourceCount,
		TargetProfileCount:         internalInfo.TargetProfileCount,
		CredentialRequirementCount: internalInfo.CredentialRequirementCount,
		Dependencies:               dependencies, BlobCount: internalInfo.BlobCount,
		BlobBytes: internalInfo.BlobBytes, SourceTrust: internalInfo.SourceTrust,
	}, nil
}
