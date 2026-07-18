import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../app/client_state.dart';
import '../../design_system/design_system.dart';

typedef TransferActionCallback = Future<void> Function(String id);

/// Presents durable transfers with state-specific, recoverable actions.
class TransferCenter extends StatefulWidget {
  const TransferCenter({
    super.key,
    required this.transfers,
    required this.onPause,
    required this.onRetry,
    required this.onDelete,
  });

  static const double maxWidth = 720;

  final List<ClientTransfer> transfers;
  final ValueChanged<String> onPause;
  final TransferActionCallback onRetry;
  final TransferActionCallback onDelete;

  @override
  State<TransferCenter> createState() => _TransferCenterState();
}

class _TransferCenterState extends State<TransferCenter> {
  final Set<String> _pendingTaskIds = <String>{};

  Future<void> _runPending(String id, TransferActionCallback action) async {
    if (!_pendingTaskIds.add(id)) {
      return;
    }
    setState(() {});
    try {
      await action(id);
    } finally {
      if (mounted && _pendingTaskIds.remove(id)) {
        setState(() {});
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final Size screenSize = MediaQuery.sizeOf(context);
    final bool compact = screenSize.width < MnemoBreakpoint.compact;
    final double height = compact
        ? screenSize.height * 0.92
        : math.min(screenSize.height * 0.82, 760);
    final List<ClientTransfer> active = widget.transfers
        .where(
          (transfer) => _groupFor(transfer.status) == _TransferGroup.active,
        )
        .toList(growable: false);
    final List<ClientTransfer> attention = widget.transfers
        .where(
          (transfer) => _groupFor(transfer.status) == _TransferGroup.attention,
        )
        .toList(growable: false);
    final List<ClientTransfer> recent = widget.transfers
        .where(
          (transfer) => _groupFor(transfer.status) == _TransferGroup.recent,
        )
        .toList(growable: false);

    return Align(
      alignment: Alignment.bottomCenter,
      child: ConstrainedBox(
        key: const Key('transfer-center-surface'),
        constraints: BoxConstraints(
          maxWidth: TransferCenter.maxWidth,
          minHeight: math.min(height, 320),
          maxHeight: height,
        ),
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: Theme.of(context).colorScheme.surface,
            borderRadius: const BorderRadius.vertical(
              top: Radius.circular(MnemoRadius.xl),
            ),
          ),
          child: SafeArea(
            top: false,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: <Widget>[
                _TransferCenterHeader(total: widget.transfers.length),
                Expanded(
                  child: widget.transfers.isEmpty
                      ? const MnemoEmptyState(
                          icon: Icons.swap_vert_rounded,
                          title: '暂无传输记录',
                          message: '上传或下载文件后，传输状态会显示在这里。',
                        )
                      : ListView(
                          key: const Key('transfer-center-list'),
                          padding: const EdgeInsets.only(
                            left: MnemoSpacing.md,
                            right: MnemoSpacing.md,
                            bottom: MnemoSpacing.lg,
                          ),
                          children: <Widget>[
                            if (active.isNotEmpty)
                              _TransferSection(
                                key: const Key('transfer-section-active'),
                                title: '进行中',
                                count: active.length,
                                transfers: active,
                                pendingTaskIds: _pendingTaskIds,
                                onPause: widget.onPause,
                                onRetry: (id) =>
                                    _runPending(id, widget.onRetry),
                                onDelete: (id) =>
                                    _runPending(id, widget.onDelete),
                              ),
                            if (attention.isNotEmpty)
                              _TransferSection(
                                key: const Key('transfer-section-attention'),
                                title: '需要处理',
                                count: attention.length,
                                transfers: attention,
                                pendingTaskIds: _pendingTaskIds,
                                onPause: widget.onPause,
                                onRetry: (id) =>
                                    _runPending(id, widget.onRetry),
                                onDelete: (id) =>
                                    _runPending(id, widget.onDelete),
                              ),
                            if (recent.isNotEmpty)
                              _TransferSection(
                                key: const Key('transfer-section-recent'),
                                title: '最近记录',
                                count: recent.length,
                                transfers: recent,
                                pendingTaskIds: _pendingTaskIds,
                                onPause: widget.onPause,
                                onRetry: (id) =>
                                    _runPending(id, widget.onRetry),
                                onDelete: (id) =>
                                    _runPending(id, widget.onDelete),
                              ),
                          ],
                        ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _TransferCenterHeader extends StatelessWidget {
  const _TransferCenterHeader({required this.total});

  final int total;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        MnemoSpacing.lg,
        MnemoSpacing.sm,
        MnemoSpacing.md,
        MnemoSpacing.md,
      ),
      child: Row(
        children: <Widget>[
          Expanded(
            child: Semantics(
              header: true,
              child: Text(
                '传输中心',
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
          ),
          if (total > 0)
            MnemoStatusPill(
              label: '$total 项',
              tone: MnemoStatusTone.neutral,
              showIcon: false,
              compact: true,
            ),
          if (Navigator.canPop(context)) ...<Widget>[
            const SizedBox(width: MnemoSpacing.xs),
            IconButton(
              onPressed: () => Navigator.pop(context),
              tooltip: '关闭传输中心',
              icon: const Icon(Icons.close_rounded),
            ),
          ],
        ],
      ),
    );
  }
}

class _TransferSection extends StatelessWidget {
  const _TransferSection({
    super.key,
    required this.title,
    required this.count,
    required this.transfers,
    required this.pendingTaskIds,
    required this.onPause,
    required this.onRetry,
    required this.onDelete,
  });

  final String title;
  final int count;
  final List<ClientTransfer> transfers;
  final Set<String> pendingTaskIds;
  final ValueChanged<String> onPause;
  final TransferActionCallback onRetry;
  final TransferActionCallback onDelete;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: MnemoSpacing.lg),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          Padding(
            padding: const EdgeInsets.fromLTRB(
              MnemoSpacing.xxs,
              MnemoSpacing.xs,
              MnemoSpacing.xxs,
              MnemoSpacing.sm,
            ),
            child: Semantics(
              header: true,
              child: Text(
                '$title · $count',
                style: Theme.of(context).textTheme.titleSmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ),
          for (var index = 0; index < transfers.length; index++) ...<Widget>[
            if (index > 0) const SizedBox(height: MnemoSpacing.xs),
            _TransferCard(
              transfer: transfers[index],
              isPending: pendingTaskIds.contains(transfers[index].id),
              onPause: onPause,
              onRetry: onRetry,
              onDelete: onDelete,
            ),
          ],
        ],
      ),
    );
  }
}

class _TransferCard extends StatelessWidget {
  const _TransferCard({
    required this.transfer,
    required this.isPending,
    required this.onPause,
    required this.onRetry,
    required this.onDelete,
  });

  final ClientTransfer transfer;
  final bool isPending;
  final ValueChanged<String> onPause;
  final TransferActionCallback onRetry;
  final TransferActionCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final String status = _transferStatusLabel(transfer);
    final String direction = transfer.direction == TransferDirection.upload
        ? '上传'
        : '下载';
    final String semanticLabel = '$direction ${transfer.name}，$status';
    final ColorScheme colors = Theme.of(context).colorScheme;

    return MnemoCard(
      key: Key('transfer-card-${transfer.id}'),
      tone: _cardToneFor(transfer.status),
      padding: const EdgeInsets.all(MnemoSpacing.md),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: <Widget>[
              Expanded(
                child: Semantics(
                  key: Key('transfer-summary-${transfer.id}'),
                  container: true,
                  label: semanticLabel,
                  child: ExcludeSemantics(
                    child: Row(
                      children: <Widget>[
                        DecoratedBox(
                          decoration: BoxDecoration(
                            color: _iconBackground(context, transfer.status),
                            borderRadius: BorderRadius.circular(MnemoRadius.sm),
                          ),
                          child: Padding(
                            padding: const EdgeInsets.all(MnemoSpacing.xs),
                            child: Icon(
                              transfer.direction == TransferDirection.upload
                                  ? Icons.upload_rounded
                                  : Icons.download_rounded,
                              size: 22,
                              color: _iconForeground(context, transfer.status),
                            ),
                          ),
                        ),
                        const SizedBox(width: MnemoSpacing.sm),
                        Expanded(
                          child: Text(
                            transfer.name,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: Theme.of(context).textTheme.titleMedium,
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
              const SizedBox(width: MnemoSpacing.xs),
              _TransferActions(
                transfer: transfer,
                isPending: isPending,
                onPause: onPause,
                onRetry: onRetry,
                onDelete: onDelete,
              ),
            ],
          ),
          const SizedBox(height: MnemoSpacing.xs),
          ExcludeSemantics(
            child: Text(
              status,
              style: Theme.of(
                context,
              ).textTheme.bodyMedium?.copyWith(color: colors.onSurfaceVariant),
            ),
          ),
          if (_showsProgress(transfer.status)) ...<Widget>[
            const SizedBox(height: MnemoSpacing.sm),
            LinearProgressIndicator(
              value: transfer.progress,
              minHeight: 6,
              borderRadius: BorderRadius.circular(MnemoRadius.pill),
              semanticsLabel: '$direction ${transfer.name}进度',
              semanticsValue: transfer.total > 0 ? _percentage(transfer) : null,
            ),
          ],
        ],
      ),
    );
  }
}

class _TransferActions extends StatelessWidget {
  const _TransferActions({
    required this.transfer,
    required this.isPending,
    required this.onPause,
    required this.onRetry,
    required this.onDelete,
  });

  final ClientTransfer transfer;
  final bool isPending;
  final ValueChanged<String> onPause;
  final TransferActionCallback onRetry;
  final TransferActionCallback onDelete;

  @override
  Widget build(BuildContext context) {
    if (isPending) {
      return Semantics(
        label: '正在处理 ${transfer.name}',
        child: const SizedBox.square(
          dimension: 48,
          child: Padding(
            padding: EdgeInsets.all(MnemoSpacing.md),
            child: CircularProgressIndicator(strokeWidth: 2.5),
          ),
        ),
      );
    }
    return switch (transfer.status) {
      TransferStatus.preparing || TransferStatus.running => IconButton(
        key: Key('transfer-pause-${transfer.id}'),
        onPressed: () => onPause(transfer.id),
        tooltip: '暂停 ${transfer.name}',
        icon: const Icon(Icons.pause_rounded),
      ),
      TransferStatus.queued => const SizedBox.square(dimension: 48),
      TransferStatus.paused || TransferStatus.awaitingAuth => Row(
        mainAxisSize: MainAxisSize.min,
        children: <Widget>[
          IconButton(
            key: Key('transfer-retry-${transfer.id}'),
            onPressed: () => unawaited(onRetry(transfer.id)),
            tooltip: '继续 ${transfer.name}',
            icon: const Icon(Icons.play_arrow_rounded),
          ),
          _DestructiveMenu(transfer: transfer, onDelete: onDelete),
        ],
      ),
      TransferStatus.awaitingDestination => Row(
        mainAxisSize: MainAxisSize.min,
        children: <Widget>[
          IconButton(
            key: Key('transfer-retry-${transfer.id}'),
            onPressed: () => unawaited(onRetry(transfer.id)),
            tooltip: '选择保存位置 ${transfer.name}',
            icon: const Icon(Icons.folder_open_rounded),
          ),
          _DestructiveMenu(transfer: transfer, onDelete: onDelete),
        ],
      ),
      TransferStatus.failed =>
        transfer.canRetry
            ? Row(
                mainAxisSize: MainAxisSize.min,
                children: <Widget>[
                  IconButton(
                    key: Key('transfer-retry-${transfer.id}'),
                    onPressed: () => unawaited(onRetry(transfer.id)),
                    tooltip: '重试 ${transfer.name}',
                    icon: const Icon(Icons.refresh_rounded),
                  ),
                  _DestructiveMenu(transfer: transfer, onDelete: onDelete),
                ],
              )
            : _DestructiveMenu(transfer: transfer, onDelete: onDelete),
      TransferStatus.resultUnconfirmed => IconButton(
        key: Key('transfer-review-remove-${transfer.id}'),
        onPressed: () => unawaited(
          _confirmResultRemoval(context, transfer, onDelete: onDelete),
        ),
        tooltip: '确认并移除 ${transfer.name}',
        icon: const Icon(Icons.fact_check_outlined),
      ),
      TransferStatus.completed || TransferStatus.cancelled => IconButton(
        key: Key('transfer-clear-${transfer.id}'),
        onPressed: () => unawaited(onDelete(transfer.id)),
        tooltip: '清除记录 ${transfer.name}',
        icon: const Icon(Icons.close_rounded),
      ),
    };
  }
}

enum _TransferMenuAction { delete }

class _DestructiveMenu extends StatelessWidget {
  const _DestructiveMenu({required this.transfer, required this.onDelete});

  final ClientTransfer transfer;
  final TransferActionCallback onDelete;

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<_TransferMenuAction>(
      key: Key('transfer-more-${transfer.id}'),
      tooltip: '更多操作 ${transfer.name}',
      onSelected: (action) {
        if (action == _TransferMenuAction.delete) {
          unawaited(
            _confirmCancellation(context, transfer, onDelete: onDelete),
          );
        }
      },
      itemBuilder: (context) => <PopupMenuEntry<_TransferMenuAction>>[
        PopupMenuItem<_TransferMenuAction>(
          key: Key('transfer-delete-${transfer.id}'),
          value: _TransferMenuAction.delete,
          child: const Row(
            children: <Widget>[
              Icon(Icons.delete_outline_rounded),
              SizedBox(width: MnemoSpacing.sm),
              Text('取消并删除'),
            ],
          ),
        ),
      ],
    );
  }
}

Future<void> _confirmCancellation(
  BuildContext context,
  ClientTransfer transfer, {
  required TransferActionCallback onDelete,
}) async {
  final bool? confirmed = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: const Text('取消并删除传输？'),
      content: Text(
        '将取消“${transfer.name}”的传输，并删除保存在本机的可恢复进度。'
        '若服务端存在上传会话，也会一并取消。此操作无法撤销。',
      ),
      actions: <Widget>[
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('保留传输'),
        ),
        FilledButton(
          key: const Key('transfer-confirm-delete'),
          onPressed: () => Navigator.pop(context, true),
          child: const Text('取消并删除'),
        ),
      ],
    ),
  );
  if (confirmed == true && context.mounted) {
    await onDelete(transfer.id);
  }
}

Future<void> _confirmResultRemoval(
  BuildContext context,
  ClientTransfer transfer, {
  required TransferActionCallback onDelete,
}) async {
  final bool? confirmed = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: const Text('移除待确认记录？'),
      content: Text(
        '“${transfer.name}”的传输结果尚未确认。移除后会删除本地暂存数据，'
        '且无法再从此记录继续核对。请先确认目标位置。',
      ),
      actions: <Widget>[
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('继续保留'),
        ),
        FilledButton(
          key: const Key('transfer-confirm-review-remove'),
          onPressed: () => Navigator.pop(context, true),
          child: const Text('确认移除'),
        ),
      ],
    ),
  );
  if (confirmed == true && context.mounted) {
    await onDelete(transfer.id);
  }
}

enum _TransferGroup { active, attention, recent }

_TransferGroup _groupFor(TransferStatus status) {
  return switch (status) {
    TransferStatus.preparing ||
    TransferStatus.queued ||
    TransferStatus.running => _TransferGroup.active,
    TransferStatus.paused ||
    TransferStatus.awaitingAuth ||
    TransferStatus.awaitingDestination ||
    TransferStatus.resultUnconfirmed ||
    TransferStatus.failed => _TransferGroup.attention,
    TransferStatus.completed ||
    TransferStatus.cancelled => _TransferGroup.recent,
  };
}

MnemoCardTone _cardToneFor(TransferStatus status) {
  return switch (status) {
    TransferStatus.preparing ||
    TransferStatus.queued ||
    TransferStatus.running => MnemoCardTone.brandTint,
    TransferStatus.paused ||
    TransferStatus.awaitingAuth ||
    TransferStatus.awaitingDestination ||
    TransferStatus.resultUnconfirmed => MnemoCardTone.muted,
    TransferStatus.failed => MnemoCardTone.standard,
    TransferStatus.completed ||
    TransferStatus.cancelled => MnemoCardTone.standard,
  };
}

Color _iconBackground(BuildContext context, TransferStatus status) {
  final ColorScheme colors = Theme.of(context).colorScheme;
  final MnemoSemanticColors semantic = context.mnemoColors;
  return switch (status) {
    TransferStatus.preparing ||
    TransferStatus.queued ||
    TransferStatus.running => colors.primaryContainer,
    TransferStatus.completed => semantic.successContainer,
    TransferStatus.failed => semantic.dangerContainer,
    TransferStatus.paused ||
    TransferStatus.awaitingAuth ||
    TransferStatus.awaitingDestination ||
    TransferStatus.resultUnconfirmed => semantic.warningContainer,
    TransferStatus.cancelled => colors.surfaceContainerHighest,
  };
}

Color _iconForeground(BuildContext context, TransferStatus status) {
  final ColorScheme colors = Theme.of(context).colorScheme;
  final MnemoSemanticColors semantic = context.mnemoColors;
  return switch (status) {
    TransferStatus.preparing ||
    TransferStatus.queued ||
    TransferStatus.running => colors.onPrimaryContainer,
    TransferStatus.completed => semantic.onSuccessContainer,
    TransferStatus.failed => semantic.onDangerContainer,
    TransferStatus.paused ||
    TransferStatus.awaitingAuth ||
    TransferStatus.awaitingDestination ||
    TransferStatus.resultUnconfirmed => semantic.onWarningContainer,
    TransferStatus.cancelled => colors.onSurfaceVariant,
  };
}

bool _showsProgress(TransferStatus status) {
  return status == TransferStatus.preparing || status == TransferStatus.running;
}

String _transferStatusLabel(ClientTransfer transfer) {
  final String progress = transfer.total > 0
      ? '${_percentage(transfer)} · '
            '${_formatBytes(transfer.transferred)} / '
            '${_formatBytes(transfer.total)}'
      : '正在传输';
  return switch (transfer.status) {
    TransferStatus.preparing =>
      transfer.total > 0 ? '正在准备上传 · $progress' : '正在准备上传',
    TransferStatus.queued => '等待传输',
    TransferStatus.running => progress,
    TransferStatus.paused => transfer.errorMessage ?? '已暂停，可继续传输',
    TransferStatus.awaitingAuth => '等待重新登录后继续',
    TransferStatus.awaitingDestination =>
      transfer.errorMessage ?? '下载完成，等待选择保存位置',
    TransferStatus.resultUnconfirmed =>
      transfer.errorMessage ?? '操作结果待确认，请核对目标位置',
    TransferStatus.completed => '已完成',
    TransferStatus.failed =>
      transfer.errorMessage ?? (transfer.canRetry ? '传输失败，可重试' : '传输失败，无法继续'),
    TransferStatus.cancelled => '已取消',
  };
}

String _percentage(ClientTransfer transfer) {
  final double value = transfer.progress ?? 0;
  return '${(value * 100).round()}%';
}

String _formatBytes(int bytes) {
  const List<String> units = <String>['B', 'KB', 'MB', 'GB', 'TB'];
  var value = bytes.toDouble();
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final int decimals = value >= 10 || unit == 0 ? 0 : 1;
  return '${value.toStringAsFixed(decimals)} ${units[unit]}';
}
