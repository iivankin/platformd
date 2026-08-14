package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/diskusage"
)

var ErrDiskPressureNotReady = errors.New("disk pressure is not ready")

type DiskPressureSnapshotter interface {
	Snapshot() (diskpressure.Snapshot, bool)
}

type DiskPressureComponents interface {
	Components(context.Context) (diskusage.Snapshot, error)
}

type DiskPressureApplication struct {
	pressure   DiskPressureSnapshotter
	components DiskPressureComponents
}

type DiskPressureDTO struct {
	Level               diskpressure.Level `json:"level"`
	ByteBasisPoints     uint64             `json:"byteBasisPoints"`
	InodeBasisPoints    uint64             `json:"inodeBasisPoints"`
	TotalBytes          uint64             `json:"totalBytes"`
	UsedBytes           uint64             `json:"usedBytes"`
	AvailableBytes      uint64             `json:"availableBytes"`
	TotalInodes         uint64             `json:"totalInodes"`
	AvailableInodes     uint64             `json:"availableInodes"`
	ReservePresent      bool               `json:"reservePresent"`
	CheckedAt           int64              `json:"checkedAt"`
	Components          []DiskComponentDTO `json:"components,omitempty"`
	ComponentsCheckedAt int64              `json:"componentsCheckedAt,omitempty"`
}

type DiskComponentDTO struct {
	ID    string `json:"id"`
	Bytes uint64 `json:"bytes"`
}

func NewDiskPressureApplication(pressure DiskPressureSnapshotter, components DiskPressureComponents) (*DiskPressureApplication, error) {
	if pressure == nil {
		return nil, errors.New("disk pressure snapshotter is required")
	}
	return &DiskPressureApplication{pressure: pressure, components: components}, nil
}

func (application *DiskPressureApplication) Read(ctx context.Context, identity Identity) (DiskPressureDTO, error) {
	if err := requireReadIdentity(identity); err != nil {
		return DiskPressureDTO{}, err
	}
	snapshot, ready := application.pressure.Snapshot()
	if !ready {
		return DiskPressureDTO{}, ErrDiskPressureNotReady
	}
	result := DiskPressureDTO{
		Level: snapshot.Level, ByteBasisPoints: snapshot.Usage.ByteBasisPoints,
		InodeBasisPoints: snapshot.Usage.InodeBasisPoints, TotalBytes: snapshot.Usage.TotalBytes,
		UsedBytes: snapshot.Usage.UsedBytes, AvailableBytes: snapshot.Usage.AvailableBytes,
		TotalInodes:     snapshot.Usage.TotalInodes,
		AvailableInodes: snapshot.Usage.AvailableInodes, ReservePresent: snapshot.ReservePresent,
		CheckedAt: snapshot.CheckedAt.UnixMilli(),
	}
	if application.components == nil {
		return result, nil
	}
	usage, err := application.components.Components(ctx)
	if err != nil {
		return result, nil
	}
	result.Components = make([]DiskComponentDTO, 0, len(usage.Components))
	for _, component := range usage.Components {
		result.Components = append(result.Components, DiskComponentDTO{ID: component.ID, Bytes: component.Bytes})
	}
	if !usage.CheckedAt.IsZero() {
		result.ComponentsCheckedAt = usage.CheckedAt.UnixMilli()
	}
	return result, nil
}
