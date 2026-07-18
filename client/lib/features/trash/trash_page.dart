import 'dart:async';

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../core/files/file_path.dart';
import '../../core/trash/trash_models.dart';
import '../../design_system/design_system.dart';

enum TrashLoadState { loading, ready, error }

enum TrashRestoreOutcome { restored, pathConflict }

final class TrashDeleteOutcome {
  TrashDeleteOutcome({
    required List<String> deletedIds,
    required List<String> skippedIds,
    required List<String> remainingIds,
  }) : deletedIds = List<String>.unmodifiable(deletedIds),
       skippedIds = List<String>.unmodifiable(skippedIds),
       remainingIds = List<String>.unmodifiable(remainingIds);

  factory TrashDeleteOutcome.allDeleted(TrashSelectionSnapshot selection) {
    return TrashDeleteOutcome(
      deletedIds: selection.ids,
      skippedIds: const <String>[],
      remainingIds: const <String>[],
    );
  }

  final List<String> deletedIds;
  final List<String> skippedIds;
  final List<String> remainingIds;

  bool get isPartial => remainingIds.isNotEmpty;
}

typedef TrashRestoreCallback =
    Future<TrashRestoreOutcome> Function(
      TrashItem item,
      String? destinationPath,
    );
typedef TrashDeleteCallback =
    Future<TrashDeleteOutcome> Function(TrashSelectionSnapshot selection);

final class TrashViewModel {
  const TrashViewModel._({
    required this.state,
    this.listing,
    this.canWrite = false,
    this.errorMessage,
    this.mutationBlockedMessage,
    this.refreshErrorMessage,
  });

  const TrashViewModel.loading() : this._(state: TrashLoadState.loading);

  const TrashViewModel.ready({
    required TrashListing listing,
    required bool canWrite,
    String? mutationBlockedMessage,
    String? refreshErrorMessage,
  }) : this._(
         state: TrashLoadState.ready,
         listing: listing,
         canWrite: canWrite,
         mutationBlockedMessage: mutationBlockedMessage,
         refreshErrorMessage: refreshErrorMessage,
       );

  const TrashViewModel.error(String message)
    : this._(state: TrashLoadState.error, errorMessage: message);

  final TrashLoadState state;
  final TrashListing? listing;
  final bool canWrite;
  final String? errorMessage;
  final String? mutationBlockedMessage;
  final String? refreshErrorMessage;
}

class TrashPage extends StatefulWidget {
  const TrashPage({
    super.key,
    required this.viewModel,
    required this.onRefresh,
    required this.onRestore,
    required this.onDeletePermanently,
    this.showSuccessMessages = true,
    this.selectionLimit = maxTrashSelectionIds,
  }) : assert(selectionLimit > 0 && selectionLimit <= maxTrashSelectionIds);

  final TrashViewModel viewModel;
  final Future<void> Function() onRefresh;
  final TrashRestoreCallback onRestore;
  final TrashDeleteCallback onDeletePermanently;
  final bool showSuccessMessages;
  final int selectionLimit;

  @override
  State<TrashPage> createState() => _TrashPageState();
}

class _TrashPageState extends State<TrashPage> {
  final Set<String> _selectedIds = <String>{};
  final Set<String> _busyIds = <String>{};
  bool _deleting = false;

  @override
  void didUpdateWidget(covariant TrashPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    final availableIds =
        widget.viewModel.listing?.items.map((item) => item.id).toSet() ??
        const <String>{};
    _selectedIds.removeWhere((id) => !availableIds.contains(id));
    _busyIds.removeWhere((id) => !availableIds.contains(id));
    if (!widget.viewModel.canWrite) {
      _selectedIds.clear();
    }
  }

  List<TrashItem> get _selectedItems {
    final items = widget.viewModel.listing?.items ?? const <TrashItem>[];
    return items
        .where((item) => _selectedIds.contains(item.id))
        .toList(growable: false);
  }

  void _toggleSelection(TrashItem item) {
    if (!widget.viewModel.canWrite || _deleting) {
      return;
    }
    if (_selectedIds.contains(item.id)) {
      setState(() {
        _selectedIds.remove(item.id);
      });
      return;
    }
    if (_selectedIds.length >= widget.selectionLimit) {
      _showMessage('每次最多选择 ${widget.selectionLimit} 项，请分批永久删除。');
      return;
    }
    setState(() => _selectedIds.add(item.id));
  }

  @override
  Widget build(BuildContext context) {
    return switch (widget.viewModel.state) {
      TrashLoadState.loading => const _TrashLoading(),
      TrashLoadState.error => _TrashError(
        message: widget.viewModel.errorMessage ?? '回收站加载失败',
        onRetry: widget.onRefresh,
      ),
      TrashLoadState.ready => _buildReady(widget.viewModel.listing!),
    };
  }

  Widget _buildReady(TrashListing listing) {
    if (listing.items.isEmpty) {
      return _TrashEmpty(
        policy: listing.policy,
        canWrite: widget.viewModel.canWrite,
        mutationBlockedMessage: widget.viewModel.mutationBlockedMessage,
        onRefresh: widget.onRefresh,
      );
    }

    return SafeArea(
      child: MnemoAdaptiveBuilder(
        builder: (context, windowClass) {
          final padding = MnemoAdaptive.pagePaddingFor(windowClass);
          final selectedItems = _selectedItems;
          return RefreshIndicator(
            onRefresh: widget.onRefresh,
            child: CustomScrollView(
              key: const Key('trash-ready'),
              physics: const AlwaysScrollableScrollPhysics(),
              slivers: <Widget>[
                SliverPadding(
                  padding: EdgeInsets.fromLTRB(
                    padding.left,
                    padding.top,
                    padding.right,
                    0,
                  ),
                  sliver: SliverToBoxAdapter(
                    child: MnemoSectionTitle(
                      title: '回收站',
                      description:
                          widget.viewModel.mutationBlockedMessage ??
                          (widget.viewModel.canWrite
                              ? '恢复误删项目，或永久清理已确认的内容'
                              : '当前账户仅可查看回收站内容'),
                      leading: const Icon(Icons.delete_outline_rounded),
                      action: selectedItems.isEmpty
                          ? null
                          : _SelectionActions(
                              count: selectedItems.length,
                              deleting: _deleting,
                              onClear: () => setState(_selectedIds.clear),
                              onDelete: () => unawaited(
                                _confirmPermanentDelete(selectedItems),
                              ),
                            ),
                    ),
                  ),
                ),
                SliverPadding(
                  padding: EdgeInsets.fromLTRB(
                    padding.left,
                    MnemoSpacing.lg,
                    padding.right,
                    0,
                  ),
                  sliver: SliverToBoxAdapter(
                    child: _TrashPolicySummary(
                      listing: listing,
                      canWrite: widget.viewModel.canWrite,
                    ),
                  ),
                ),
                if (widget.viewModel.mutationBlockedMessage case final message?)
                  SliverPadding(
                    padding: EdgeInsets.fromLTRB(
                      padding.left,
                      MnemoSpacing.md,
                      padding.right,
                      0,
                    ),
                    sliver: SliverToBoxAdapter(
                      child: MnemoErrorNotice(
                        key: const Key('trash-reconciliation-required'),
                        title: '需要核对上一次操作结果',
                        message: message,
                        onRetry: () => unawaited(widget.onRefresh()),
                      ),
                    ),
                  ),
                if (widget.viewModel.refreshErrorMessage case final message?)
                  SliverPadding(
                    padding: EdgeInsets.fromLTRB(
                      padding.left,
                      MnemoSpacing.md,
                      padding.right,
                      0,
                    ),
                    sliver: SliverToBoxAdapter(
                      child: MnemoErrorNotice(
                        key: const Key('trash-refresh-error'),
                        title: '回收站刷新失败',
                        message: '当前显示上一次成功加载的数据。$message',
                        onRetry: () => unawaited(widget.onRefresh()),
                      ),
                    ),
                  ),
                SliverPadding(
                  padding: EdgeInsets.fromLTRB(
                    padding.left,
                    MnemoSpacing.lg,
                    padding.right,
                    padding.bottom,
                  ),
                  sliver: windowClass == MnemoWindowClass.compact
                      ? SliverList.separated(
                          key: const Key('trash-list'),
                          itemCount: listing.items.length,
                          itemBuilder: (context, index) =>
                              _buildItem(listing.items[index], listing.policy),
                          separatorBuilder: (context, index) =>
                              const SizedBox(height: MnemoSpacing.sm),
                        )
                      : SliverToBoxAdapter(
                          child: _TrashGrid(
                            key: const Key('trash-grid'),
                            items: listing.items,
                            columns: windowClass == MnemoWindowClass.expanded
                                ? 3
                                : 2,
                            itemBuilder: (item) =>
                                _buildItem(item, listing.policy),
                          ),
                        ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _buildItem(TrashItem item, TrashPolicySnapshot policy) {
    return _TrashItemCard(
      item: item,
      policy: policy,
      canWrite: widget.viewModel.canWrite,
      selected: _selectedIds.contains(item.id),
      busy: _busyIds.contains(item.id),
      deleting: _deleting,
      onToggleSelection: () => _toggleSelection(item),
      onRestore: () => unawaited(_restore(item)),
      onDelete: () => unawaited(_confirmPermanentDelete(<TrashItem>[item])),
    );
  }

  Future<void> _restore(TrashItem item) async {
    if (!widget.viewModel.canWrite || _busyIds.contains(item.id) || _deleting) {
      return;
    }
    setState(() => _busyIds.add(item.id));
    var restored = false;
    try {
      final outcome = await widget.onRestore(item, null);
      if (!mounted) {
        return;
      }
      if (outcome == TrashRestoreOutcome.pathConflict) {
        setState(() => _busyIds.remove(item.id));
        restored = await _showCustomRestoreDialog(item);
      } else {
        restored = true;
      }
      if (!mounted || !restored) {
        return;
      }
      setState(() => _selectedIds.remove(item.id));
      if (widget.showSuccessMessages) {
        _showMessage('“${item.name}”已恢复。');
      }
    } on Object catch (error) {
      if (mounted) {
        _showMessage(_actionErrorMessage(error, fallback: '恢复未完成，请稍后重试。'));
      }
    } finally {
      if (mounted) {
        setState(() => _busyIds.remove(item.id));
      }
    }
  }

  Future<bool> _showCustomRestoreDialog(TrashItem item) async {
    return await showDialog<bool>(
          context: context,
          barrierDismissible: false,
          builder: (dialogContext) =>
              _CustomRestoreDialog(item: item, onRestore: widget.onRestore),
        ) ??
        false;
  }

  Future<void> _confirmPermanentDelete(List<TrashItem> requestedItems) async {
    if (!widget.viewModel.canWrite || requestedItems.isEmpty || _deleting) {
      return;
    }
    final frozenItems = List<TrashItem>.unmodifiable(requestedItems);
    late final TrashSelectionSnapshot snapshot;
    try {
      snapshot = TrashSelectionSnapshot.fromItems(frozenItems);
    } on FormatException {
      _showMessage('每次最多选择 $maxTrashSelectionIds 项，请分批永久删除。');
      return;
    }
    final totalSize = frozenItems.fold<int>(0, (sum, item) => sum + item.size);
    final confirmed =
        await showDialog<bool>(
          context: context,
          builder: (dialogContext) => AlertDialog(
            icon: Icon(
              Icons.delete_forever_rounded,
              color: Theme.of(dialogContext).colorScheme.error,
            ),
            title: const Text('永久删除已选项目？'),
            content: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 520),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: <Widget>[
                  Text(
                    '${frozenItems.length} 项、${_formatBytes(totalSize)}'
                    ' 将被永久删除，且无法恢复。',
                  ),
                  const SizedBox(height: MnemoSpacing.sm),
                  for (final item in frozenItems.take(3))
                    Padding(
                      padding: const EdgeInsets.only(bottom: MnemoSpacing.xxs),
                      child: Text(
                        '• ${item.name}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  if (frozenItems.length > 3)
                    Text('另有 ${frozenItems.length - 3} 项'),
                ],
              ),
            ),
            actions: <Widget>[
              TextButton(
                onPressed: () => Navigator.pop(dialogContext, false),
                style: _dialogButtonStyle(),
                child: const Text('取消'),
              ),
              FilledButton(
                key: const Key('trash-delete-confirm'),
                onPressed: () => Navigator.pop(dialogContext, true),
                style: _dialogButtonStyle(
                  backgroundColor: Theme.of(dialogContext).colorScheme.error,
                  foregroundColor: Theme.of(dialogContext).colorScheme.onError,
                ),
                child: const Text('永久删除'),
              ),
            ],
          ),
        ) ??
        false;
    if (!confirmed || !mounted) {
      return;
    }

    setState(() => _deleting = true);
    try {
      final outcome = await widget.onDeletePermanently(snapshot);
      if (!mounted) {
        return;
      }
      setState(() {
        _selectedIds.removeAll(outcome.deletedIds);
        _selectedIds.removeAll(outcome.skippedIds);
        _selectedIds.addAll(outcome.remainingIds);
      });
      if (widget.showSuccessMessages) {
        _showMessage(
          outcome.isPartial
              ? '已永久删除 ${outcome.deletedIds.length} 项，'
                    '${outcome.remainingIds.length} 项仍待处理。'
              : outcome.skippedIds.isNotEmpty
              ? '已永久删除 ${outcome.deletedIds.length} 项，'
                    '${outcome.skippedIds.length} 项已不存在。'
              : '已永久删除 ${outcome.deletedIds.length} 项。',
        );
      }
    } on Object catch (error) {
      if (mounted) {
        _showMessage(
          _actionErrorMessage(error, fallback: '永久删除未完成，请刷新回收站核对结果。'),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _deleting = false);
      }
    }
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }
}

class _CustomRestoreDialog extends StatefulWidget {
  const _CustomRestoreDialog({required this.item, required this.onRestore});

  final TrashItem item;
  final TrashRestoreCallback onRestore;

  @override
  State<_CustomRestoreDialog> createState() => _CustomRestoreDialogState();
}

class _CustomRestoreDialogState extends State<_CustomRestoreDialog> {
  late final TextEditingController _controller;
  bool _submitting = false;
  String? _errorMessage;

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: widget.item.originalPath);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    String destination;
    try {
      destination = normalizeLogicalPath(
        _controller.text.trim(),
        allowRoot: false,
      );
    } on FormatException catch (error) {
      setState(() => _errorMessage = error.message);
      return;
    }

    setState(() {
      _submitting = true;
      _errorMessage = null;
    });
    try {
      final outcome = await widget.onRestore(widget.item, destination);
      if (!mounted) {
        return;
      }
      if (outcome == TrashRestoreOutcome.pathConflict) {
        setState(() {
          _submitting = false;
          _errorMessage = '目标路径已存在，请更换文件名或目录。';
        });
        return;
      }
      Navigator.pop(context, true);
    } on Object catch (error) {
      if (mounted) {
        setState(() {
          _submitting = false;
          _errorMessage = _actionErrorMessage(error, fallback: '恢复未完成，请稍后重试。');
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return PopScope(
      canPop: !_submitting,
      child: AlertDialog(
        title: const Text('选择新的恢复位置'),
        content: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 520),
          child: TextField(
            key: const Key('trash-restore-custom-path'),
            controller: _controller,
            autofocus: true,
            enabled: !_submitting,
            decoration: InputDecoration(
              labelText: '目标路径',
              helperText: '输入以 / 开头的文件或文件夹完整路径',
              errorText: _errorMessage,
            ),
            textInputAction: TextInputAction.done,
            onSubmitted: _submitting ? null : (_) => _submit(),
          ),
        ),
        actions: <Widget>[
          TextButton(
            onPressed: _submitting ? null : () => Navigator.pop(context, false),
            style: _dialogButtonStyle(),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const Key('trash-restore-custom-submit'),
            onPressed: _submitting ? null : _submit,
            style: _dialogButtonStyle(),
            child: _submitting
                ? const SizedBox.square(
                    dimension: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Text('恢复到此处'),
          ),
        ],
      ),
    );
  }
}

class _TrashLoading extends StatelessWidget {
  const _TrashLoading();

  @override
  Widget build(BuildContext context) {
    return const MnemoContentFrame(
      child: Column(
        key: Key('trash-loading'),
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          MnemoSkeleton(width: 180, height: 28),
          SizedBox(height: MnemoSpacing.lg),
          MnemoSkeleton(height: 112),
          SizedBox(height: MnemoSpacing.lg),
          MnemoSkeleton(height: 176),
          SizedBox(height: MnemoSpacing.sm),
          MnemoSkeleton(height: 176),
        ],
      ),
    );
  }
}

class _TrashError extends StatelessWidget {
  const _TrashError({required this.message, required this.onRetry});

  final String message;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return MnemoContentFrame(
      alignment: Alignment.center,
      child: MnemoErrorNotice(
        key: const Key('trash-error'),
        title: '回收站加载失败',
        message: message,
        onRetry: () => unawaited(onRetry()),
      ),
    );
  }
}

class _TrashEmpty extends StatelessWidget {
  const _TrashEmpty({
    required this.policy,
    required this.canWrite,
    required this.mutationBlockedMessage,
    required this.onRefresh,
  });

  final TrashPolicySnapshot policy;
  final bool canWrite;
  final String? mutationBlockedMessage;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: LayoutBuilder(
        builder: (context, constraints) => RefreshIndicator(
          onRefresh: onRefresh,
          child: ListView(
            key: const Key('trash-empty'),
            physics: const AlwaysScrollableScrollPhysics(),
            padding: MnemoAdaptive.pagePaddingFor(
              MnemoAdaptive.windowClassFor(constraints.maxWidth),
            ),
            children: <Widget>[
              SizedBox(
                height: constraints.maxHeight > 160
                    ? constraints.maxHeight - 80
                    : 160,
                child: MnemoEmptyState(
                  icon: Icons.delete_sweep_outlined,
                  title: '回收站为空',
                  message:
                      mutationBlockedMessage ??
                      _emptyDescription(policy, canWrite),
                  primaryAction: OutlinedButton.icon(
                    onPressed: () => unawaited(onRefresh()),
                    icon: const Icon(Icons.refresh_rounded),
                    label: const Text('刷新'),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _TrashPolicySummary extends StatelessWidget {
  const _TrashPolicySummary({required this.listing, required this.canWrite});

  final TrashListing listing;
  final bool canWrite;

  @override
  Widget build(BuildContext context) {
    final policy = listing.policy;
    return MnemoCard(
      key: const Key('trash-policy-summary'),
      tone: MnemoCardTone.brandTint,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              const Icon(Icons.shield_outlined, size: 24),
              const SizedBox(width: MnemoSpacing.sm),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: <Widget>[
                    Text(
                      '删除与保留策略',
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                    const SizedBox(height: MnemoSpacing.xxs),
                    Text(
                      _policyDescription(policy),
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: MnemoSpacing.md),
          Wrap(
            spacing: MnemoSpacing.xs,
            runSpacing: MnemoSpacing.xs,
            children: <Widget>[
              MnemoStatusPill(
                label: '${listing.count} 项',
                icon: Icons.inventory_2_outlined,
                tone: MnemoStatusTone.info,
              ),
              MnemoStatusPill(
                label: _formatBytes(listing.totalSize),
                icon: Icons.data_usage_rounded,
              ),
              MnemoStatusPill(
                label: policy.retentionEnabled ? '新删除进入回收站' : '新删除为永久删除',
                icon: policy.retentionEnabled
                    ? Icons.restore_from_trash_outlined
                    : Icons.delete_forever_outlined,
                tone: policy.retentionEnabled
                    ? MnemoStatusTone.success
                    : MnemoStatusTone.warning,
              ),
              if (!canWrite)
                const MnemoStatusPill(
                  label: '只读访问',
                  icon: Icons.visibility_outlined,
                  tone: MnemoStatusTone.neutral,
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _TrashGrid extends StatelessWidget {
  const _TrashGrid({
    super.key,
    required this.items,
    required this.columns,
    required this.itemBuilder,
  });

  final List<TrashItem> items;
  final int columns;
  final Widget Function(TrashItem item) itemBuilder;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) {
        final spacing = MnemoSpacing.md;
        final width =
            (constraints.maxWidth - spacing * (columns - 1)) / columns;
        return Wrap(
          spacing: spacing,
          runSpacing: spacing,
          children: <Widget>[
            for (final item in items)
              SizedBox(width: width, child: itemBuilder(item)),
          ],
        );
      },
    );
  }
}

class _TrashItemCard extends StatelessWidget {
  const _TrashItemCard({
    required this.item,
    required this.policy,
    required this.canWrite,
    required this.selected,
    required this.busy,
    required this.deleting,
    required this.onToggleSelection,
    required this.onRestore,
    required this.onDelete,
  });

  final TrashItem item;
  final TrashPolicySnapshot policy;
  final bool canWrite;
  final bool selected;
  final bool busy;
  final bool deleting;
  final VoidCallback onToggleSelection;
  final VoidCallback onRestore;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final disabled = busy || deleting;
    return MnemoCard(
      key: Key('trash-item-${item.id}'),
      tone: selected ? MnemoCardTone.brandTint : MnemoCardTone.elevated,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              if (canWrite)
                SizedBox.square(
                  dimension: MnemoControlSize.minimumTouchTarget,
                  child: Checkbox(
                    key: Key('trash-select-${item.id}'),
                    value: selected,
                    onChanged: disabled ? null : (_) => onToggleSelection(),
                    semanticLabel: '选择 ${item.name}',
                  ),
                )
              else
                _TrashTypeIcon(isDirectory: item.isDirectory),
              if (canWrite) const SizedBox(width: MnemoSpacing.xs),
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.only(top: MnemoSpacing.xs),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: <Widget>[
                      Text(
                        item.name,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                      const SizedBox(height: MnemoSpacing.xxs),
                      Text(
                        item.originalPath,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          color: colors.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
              if (canWrite) ...<Widget>[
                SizedBox.square(
                  dimension: MnemoControlSize.minimumTouchTarget,
                  child: IconButton(
                    key: Key('trash-restore-${item.id}'),
                    onPressed: disabled ? null : onRestore,
                    tooltip: '恢复 ${item.name}',
                    icon: busy
                        ? const SizedBox.square(
                            dimension: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Icon(Icons.restore_rounded),
                  ),
                ),
                SizedBox.square(
                  dimension: MnemoControlSize.minimumTouchTarget,
                  child: IconButton(
                    key: Key('trash-delete-${item.id}'),
                    onPressed: disabled ? null : onDelete,
                    tooltip: '永久删除 ${item.name}',
                    color: colors.error,
                    icon: const Icon(Icons.delete_forever_outlined),
                  ),
                ),
              ],
            ],
          ),
          const SizedBox(height: MnemoSpacing.sm),
          Wrap(
            spacing: MnemoSpacing.xs,
            runSpacing: MnemoSpacing.xs,
            children: <Widget>[
              MnemoStatusPill(
                label: item.isDirectory ? '文件夹' : '文件',
                icon: item.isDirectory
                    ? Icons.folder_outlined
                    : Icons.insert_drive_file_outlined,
                compact: true,
              ),
              if (item.hadVersions)
                const MnemoStatusPill(
                  label: '含历史版本',
                  icon: Icons.history_rounded,
                  tone: MnemoStatusTone.info,
                  compact: true,
                ),
              MnemoStatusPill(
                label: _formatBytes(item.size),
                showIcon: false,
                compact: true,
              ),
            ],
          ),
          const SizedBox(height: MnemoSpacing.sm),
          _MetadataLine(
            icon: Icons.delete_outline_rounded,
            label: '删除于 ${_formatDateTime(item.deletedAt)}',
          ),
          const SizedBox(height: MnemoSpacing.xs),
          _MetadataLine(
            icon: Icons.schedule_rounded,
            label: _expiryLabel(item, policy),
            tone: _expiryTone(item, policy),
          ),
        ],
      ),
    );
  }
}

class _TrashTypeIcon extends StatelessWidget {
  const _TrashTypeIcon({required this.isDirectory});

  final bool isDirectory;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return SizedBox.square(
      dimension: MnemoControlSize.minimumTouchTarget,
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: colors.primaryContainer,
          borderRadius: BorderRadius.circular(MnemoRadius.sm),
        ),
        child: Icon(
          isDirectory ? Icons.folder_rounded : Icons.description_outlined,
          color: colors.onPrimaryContainer,
        ),
      ),
    );
  }
}

class _MetadataLine extends StatelessWidget {
  const _MetadataLine({
    required this.icon,
    required this.label,
    this.tone = MnemoStatusTone.neutral,
  });

  final IconData icon;
  final String label;
  final MnemoStatusTone tone;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final color = switch (tone) {
      MnemoStatusTone.warning => context.mnemoColors.warning,
      MnemoStatusTone.danger => colors.error,
      _ => colors.onSurfaceVariant,
    };
    return Row(
      children: <Widget>[
        Icon(icon, size: 16, color: color),
        const SizedBox(width: MnemoSpacing.xs),
        Expanded(
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: Theme.of(
              context,
            ).textTheme.bodySmall?.copyWith(color: color),
          ),
        ),
      ],
    );
  }
}

class _SelectionActions extends StatelessWidget {
  const _SelectionActions({
    required this.count,
    required this.deleting,
    required this.onClear,
    required this.onDelete,
  });

  final int count;
  final bool deleting;
  final VoidCallback onClear;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    return Wrap(
      spacing: MnemoSpacing.xs,
      runSpacing: MnemoSpacing.xs,
      crossAxisAlignment: WrapCrossAlignment.center,
      children: <Widget>[
        TextButton.icon(
          onPressed: deleting ? null : onClear,
          style: TextButton.styleFrom(
            minimumSize: const Size(
              MnemoControlSize.minimumTouchTarget,
              MnemoControlSize.minimumTouchTarget,
            ),
          ),
          icon: const Icon(Icons.close_rounded),
          label: Text('已选择 $count 项'),
        ),
        FilledButton.icon(
          key: const Key('trash-delete-selection'),
          onPressed: deleting ? null : onDelete,
          style: FilledButton.styleFrom(
            minimumSize: const Size(
              MnemoControlSize.minimumTouchTarget,
              MnemoControlSize.minimumTouchTarget,
            ),
            backgroundColor: Theme.of(context).colorScheme.error,
            foregroundColor: Theme.of(context).colorScheme.onError,
          ),
          icon: const Icon(Icons.delete_forever_rounded),
          label: const Text('永久删除'),
        ),
      ],
    );
  }
}

ButtonStyle _dialogButtonStyle({
  Color? backgroundColor,
  Color? foregroundColor,
}) {
  return ButtonStyle(
    minimumSize: const WidgetStatePropertyAll<Size>(
      Size(
        MnemoControlSize.minimumTouchTarget,
        MnemoControlSize.minimumTouchTarget,
      ),
    ),
    backgroundColor: backgroundColor == null
        ? null
        : WidgetStatePropertyAll<Color>(backgroundColor),
    foregroundColor: foregroundColor == null
        ? null
        : WidgetStatePropertyAll<Color>(foregroundColor),
  );
}

String _emptyDescription(TrashPolicySnapshot policy, bool canWrite) {
  if (!canWrite) {
    return '当前账户没有可查看的回收站项目，且仅具备只读权限。';
  }
  if (!policy.retentionEnabled) {
    return '当前删除方式为永久删除，新删除项目不会进入回收站。';
  }
  return '新删除项目会进入回收站，并按各自记录的到期时间保留。';
}

String _policyDescription(TrashPolicySnapshot policy) {
  if (!policy.retentionEnabled) {
    return '当前新删除项目会被永久删除。已有回收站项目仍以各自记录的到期时间为准。';
  }
  if (!policy.autoCleanupEnabled) {
    return '新删除项目进入回收站；按到期时间自动清理未启用，容量不足时仍可能提前清理。';
  }
  final capacity = policy.retentionMaxSize > 0
      ? '，回收站上限为 ${_formatBytes(policy.retentionMaxSize)}'
      : '';
  return '新删除项目计划保留 ${policy.retentionDays} 天$capacity。'
      '已有项目以各自记录的到期时间为准，容量不足时可能提前清理。';
}

String _expiryLabel(TrashItem item, TrashPolicySnapshot policy) {
  final timestamp = _formatDateTime(item.expiresAt);
  if (!policy.autoCleanupEnabled) {
    return '记录到期 $timestamp · 自动清理未启用';
  }
  if (!item.expiresAt.isAfter(DateTime.now().toUtc())) {
    return '已到期，等待清理';
  }
  return '到期于 $timestamp';
}

MnemoStatusTone _expiryTone(TrashItem item, TrashPolicySnapshot policy) {
  if (!policy.autoCleanupEnabled) {
    return MnemoStatusTone.warning;
  }
  if (!item.expiresAt.isAfter(DateTime.now().toUtc())) {
    return MnemoStatusTone.danger;
  }
  return MnemoStatusTone.neutral;
}

String _formatDateTime(DateTime value) {
  return DateFormat('yyyy-MM-dd HH:mm').format(value.toLocal());
}

String _formatBytes(int bytes) {
  if (bytes < 1024) {
    return '$bytes B';
  }
  const units = <String>['KiB', 'MiB', 'GiB', 'TiB'];
  var value = bytes.toDouble();
  var unit = -1;
  do {
    value /= 1024;
    unit++;
  } while (value >= 1024 && unit < units.length - 1);
  return '${value.toStringAsFixed(value >= 10 ? 1 : 2)} ${units[unit]}';
}

String _actionErrorMessage(Object error, {required String fallback}) {
  if (error is FormatException && error.message.isNotEmpty) {
    return error.message;
  }
  return fallback;
}
