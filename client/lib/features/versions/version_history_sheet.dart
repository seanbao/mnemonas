import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../../core/files/file_models.dart';
import '../../core/network/api_error.dart';
import '../../design_system/design_system.dart';

typedef VersionHistoryLoader = Future<FileVersionHistory> Function();
typedef VersionHistoryAction = Future<void> Function(FileVersion version);
typedef VersionHistoryRestore =
    Future<FileVersionHistory> Function(
      FileVersion version,
      String expectedCurrentHash,
    );
typedef VersionHistoryErrorMessageBuilder = String Function(Object error);

/// Presents one file's current and historical versions in an adaptive sheet.
class VersionHistorySheet extends StatefulWidget {
  const VersionHistorySheet({
    super.key,
    required this.entry,
    required this.onLoad,
    required this.onPreview,
    required this.onDownload,
    this.onRestore,
    this.errorMessageBuilder,
    this.onClose,
  });

  static const double maxWidth = 720;

  final FileEntry entry;
  final VersionHistoryLoader onLoad;
  final VersionHistoryAction onPreview;
  final VersionHistoryAction onDownload;
  final VersionHistoryRestore? onRestore;
  final VersionHistoryErrorMessageBuilder? errorMessageBuilder;
  final VoidCallback? onClose;

  @override
  State<VersionHistorySheet> createState() => _VersionHistorySheetState();
}

class _VersionHistorySheetState extends State<VersionHistorySheet> {
  FileVersionHistory? _history;
  String? _loadErrorMessage;
  String? _refreshErrorMessage;
  String? _pendingActionKey;
  bool _loading = true;
  bool _refreshing = false;
  bool _restoreBlocked = false;
  int _loadGeneration = 0;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  @override
  void didUpdateWidget(covariant VersionHistorySheet oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.entry.path == widget.entry.path) {
      return;
    }
    _loadGeneration++;
    _history = null;
    _loadErrorMessage = null;
    _refreshErrorMessage = null;
    _pendingActionKey = null;
    _loading = true;
    _refreshing = false;
    _restoreBlocked = false;
    unawaited(_load());
  }

  @override
  void dispose() {
    _loadGeneration++;
    super.dispose();
  }

  Future<void> _load({bool refresh = false}) async {
    if (refresh && (_refreshing || _loading || _pendingActionKey != null)) {
      return;
    }
    final generation = ++_loadGeneration;
    if (mounted) {
      setState(() {
        if (refresh) {
          _refreshing = true;
          _refreshErrorMessage = null;
        } else {
          _loading = true;
          _loadErrorMessage = null;
        }
      });
    }

    try {
      final history = await widget.onLoad();
      if (history.path != widget.entry.path) {
        throw const FormatException(
          'Version history belongs to a different file',
        );
      }
      if (!mounted || generation != _loadGeneration) {
        return;
      }
      setState(() {
        _history = history;
        _loading = false;
        _refreshing = false;
        _loadErrorMessage = null;
        _refreshErrorMessage = null;
      });
    } on Object catch (error) {
      if (!mounted || generation != _loadGeneration) {
        return;
      }
      final message = _errorMessage(error, fallback: '无法加载版本历史，请稍后重试。');
      setState(() {
        _loading = false;
        _refreshing = false;
        if (_history == null) {
          _loadErrorMessage = message;
        } else {
          _refreshErrorMessage = message;
        }
      });
    }
  }

  String _errorMessage(Object error, {required String fallback}) {
    final builder = widget.errorMessageBuilder;
    if (builder == null) {
      return fallback;
    }
    final message = builder(error).trim();
    return message.isEmpty ? fallback : message;
  }

  Future<void> _runAction({
    required String action,
    required String failureMessage,
    required FileVersion version,
    required VersionHistoryAction callback,
  }) async {
    if (_loading || _refreshing || _pendingActionKey != null) {
      return;
    }
    final key = '$action:${version.sequence}:${version.hash}';
    setState(() => _pendingActionKey = key);
    try {
      await callback(version);
    } on Object catch (error) {
      if (mounted) {
        _showMessage(_errorMessage(error, fallback: failureMessage));
      }
    } finally {
      if (mounted && _pendingActionKey == key) {
        setState(() => _pendingActionKey = null);
      }
    }
  }

  Future<void> _confirmRestore(FileVersion version) async {
    final restore = widget.onRestore;
    final expectedCurrentHash = _history?.current.hash;
    if (restore == null ||
        expectedCurrentHash == null ||
        version.isCurrent ||
        _loading ||
        _refreshing ||
        _pendingActionKey != null ||
        _restoreBlocked) {
      return;
    }
    final result = await showDialog<_RestoreDialogResult>(
      context: context,
      barrierDismissible: false,
      builder: (context) => _VersionRestoreDialog(
        entry: widget.entry,
        version: version,
        onRestore: (selected) {
          _loadGeneration++;
          if (mounted) {
            setState(() => _refreshing = false);
          }
          return restore(selected, expectedCurrentHash);
        },
        errorMessageBuilder: widget.errorMessageBuilder,
      ),
    );
    if (!mounted || result == null) {
      return;
    }
    if (result.requiresRefresh) {
      setState(() => _restoreBlocked = true);
      _showMessage('恢复结果需要核对。请关闭并刷新目录后重新打开版本历史。');
      return;
    }
    final history = result.history;
    if (history == null) {
      return;
    }
    if (history.path != widget.entry.path) {
      _showMessage('版本已恢复，但刷新结果与当前文件不一致，请重新打开版本历史。');
      return;
    }
    final changedAgain = history.current.hash != version.hash;
    setState(() {
      _history = history;
      _loadErrorMessage = null;
      _refreshErrorMessage = null;
    });
    _showMessage(changedAgain ? '所选版本已恢复，但文件随后再次发生变化，当前历史已刷新。' : '已恢复到所选版本。');
  }

  void _showMessage(String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  void _close() {
    final callback = widget.onClose;
    if (callback != null) {
      callback();
      return;
    }
    unawaited(Navigator.maybePop(context).then<void>((_) {}));
  }

  @override
  Widget build(BuildContext context) {
    final screenSize = MediaQuery.sizeOf(context);
    final compact = screenSize.width < MnemoBreakpoint.compact;
    final height = compact
        ? screenSize.height * 0.92
        : math.min(screenSize.height * 0.82, 760.0);
    final busy = _pendingActionKey != null;

    return PopScope(
      canPop: !busy,
      child: Semantics(
        scopesRoute: true,
        namesRoute: true,
        explicitChildNodes: true,
        label: '${widget.entry.name}的版本历史',
        child: Align(
          alignment: Alignment.bottomCenter,
          child: ConstrainedBox(
            key: const Key('version-history-surface'),
            constraints: BoxConstraints(
              maxWidth: VersionHistorySheet.maxWidth,
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
                    _VersionHistoryHeader(
                      fileName: widget.entry.name,
                      count: _history?.versions.length,
                      refreshing: _refreshing,
                      refreshEnabled: !_loading && !_refreshing && !busy,
                      closeEnabled: !busy,
                      onRefresh: () => unawaited(_load(refresh: true)),
                      onClose: _close,
                    ),
                    Expanded(child: _buildContent()),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildContent() {
    if (_loading && _history == null) {
      return const _VersionHistoryLoading();
    }
    final history = _history;
    if (history == null) {
      return _VersionHistoryError(
        message: _loadErrorMessage ?? '无法加载版本历史，请稍后重试。',
        onRetry: () => unawaited(_load()),
      );
    }

    final historicalVersions = history.versions
        .where((version) => !version.isCurrent)
        .toList(growable: false);
    return ListView(
      key: Key(
        historicalVersions.isEmpty
            ? 'version-history-current-only'
            : 'version-history-list',
      ),
      padding: const EdgeInsets.fromLTRB(
        MnemoSpacing.md,
        0,
        MnemoSpacing.md,
        MnemoSpacing.lg,
      ),
      children: <Widget>[
        Text(
          widget.entry.path,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
        if (_refreshErrorMessage case final message?) ...<Widget>[
          const SizedBox(height: MnemoSpacing.md),
          MnemoErrorNotice(
            title: '刷新版本历史失败',
            message: '$message 当前仍显示上一次成功加载的记录。',
            onRetry: () => unawaited(_load(refresh: true)),
          ),
        ],
        if (_restoreBlocked) ...<Widget>[
          const SizedBox(height: MnemoSpacing.md),
          const MnemoErrorNotice(
            key: Key('version-restore-blocked'),
            title: '需要核对恢复结果',
            message:
                '服务器可能已经完成恢复。为避免重复提交，当前面板已停用恢复操作。'
                '请关闭并刷新目录后重新打开版本历史。',
          ),
        ],
        const SizedBox(height: MnemoSpacing.md),
        if (historicalVersions.isEmpty) ...<Widget>[
          const MnemoCard(
            key: Key('version-history-current-only-notice'),
            tone: MnemoCardTone.brandTint,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: <Widget>[
                Icon(Icons.history_toggle_off_rounded),
                SizedBox(width: MnemoSpacing.sm),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: <Widget>[
                      Text('尚无历史版本'),
                      SizedBox(height: MnemoSpacing.xxs),
                      Text('当前文件发生更新并由服务器保留旧内容后，历史版本会显示在这里。'),
                    ],
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: MnemoSpacing.sm),
        ],
        Semantics(
          container: true,
          label: '版本历史，共 ${history.versions.length} 个版本',
          child: Column(
            children: <Widget>[
              for (var index = 0; index < history.versions.length; index++) ...[
                if (index > 0) const SizedBox(height: MnemoSpacing.sm),
                _VersionCard(
                  version: history.versions[index],
                  currentHash: history.current.hash,
                  pendingActionKey: _pendingActionKey,
                  actionsEnabled:
                      !_loading && !_refreshing && _pendingActionKey == null,
                  canRestore: widget.onRestore != null && !_restoreBlocked,
                  onPreview: (version) => unawaited(
                    _runAction(
                      action: 'preview',
                      failureMessage: '预览版本失败，请稍后重试。',
                      version: version,
                      callback: widget.onPreview,
                    ),
                  ),
                  onDownload: (version) => unawaited(
                    _runAction(
                      action: 'download',
                      failureMessage: '下载版本失败，请稍后重试。',
                      version: version,
                      callback: widget.onDownload,
                    ),
                  ),
                  onRestore: (version) => unawaited(_confirmRestore(version)),
                ),
              ],
            ],
          ),
        ),
      ],
    );
  }
}

class _VersionHistoryHeader extends StatelessWidget {
  const _VersionHistoryHeader({
    required this.fileName,
    required this.count,
    required this.refreshing,
    required this.refreshEnabled,
    required this.closeEnabled,
    required this.onRefresh,
    required this.onClose,
  });

  final String fileName;
  final int? count;
  final bool refreshing;
  final bool refreshEnabled;
  final bool closeEnabled;
  final VoidCallback onRefresh;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        MnemoSpacing.lg,
        MnemoSpacing.sm,
        MnemoSpacing.xs,
        MnemoSpacing.md,
      ),
      child: Row(
        children: <Widget>[
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: <Widget>[
                Semantics(
                  header: true,
                  child: Text(
                    '版本历史',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                const SizedBox(height: MnemoSpacing.xxs),
                Text(
                  fileName,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
          ),
          if (count case final value?)
            MnemoStatusPill(
              label: '$value 个版本',
              showIcon: false,
              compact: true,
            ),
          const SizedBox(width: MnemoSpacing.xxs),
          if (refreshing)
            Semantics(
              liveRegion: true,
              label: '正在刷新版本历史',
              child: const SizedBox.square(
                dimension: MnemoControlSize.minimumTouchTarget,
                child: Padding(
                  padding: EdgeInsets.all(MnemoSpacing.md),
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              ),
            )
          else
            IconButton(
              key: const Key('version-history-refresh'),
              onPressed: refreshEnabled ? onRefresh : null,
              tooltip: '刷新版本历史',
              icon: const Icon(Icons.refresh_rounded),
            ),
          IconButton(
            key: const Key('version-history-close'),
            onPressed: closeEnabled ? onClose : null,
            tooltip: '关闭版本历史',
            icon: const Icon(Icons.close_rounded),
          ),
        ],
      ),
    );
  }
}

class _VersionHistoryLoading extends StatelessWidget {
  const _VersionHistoryLoading();

  @override
  Widget build(BuildContext context) {
    return const Padding(
      key: Key('version-history-loading'),
      padding: EdgeInsets.fromLTRB(
        MnemoSpacing.md,
        0,
        MnemoSpacing.md,
        MnemoSpacing.lg,
      ),
      child: MnemoSkeletonList(itemCount: 4),
    );
  }
}

class _VersionHistoryError extends StatelessWidget {
  const _VersionHistoryError({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Padding(
      key: const Key('version-history-error'),
      padding: const EdgeInsets.all(MnemoSpacing.md),
      child: Center(
        child: MnemoErrorNotice(
          title: '版本历史加载失败',
          message: message,
          onRetry: onRetry,
        ),
      ),
    );
  }
}

class _VersionCard extends StatelessWidget {
  const _VersionCard({
    required this.version,
    required this.currentHash,
    required this.pendingActionKey,
    required this.actionsEnabled,
    required this.canRestore,
    required this.onPreview,
    required this.onDownload,
    required this.onRestore,
  });

  final FileVersion version;
  final String currentHash;
  final String? pendingActionKey;
  final bool actionsEnabled;
  final bool canRestore;
  final ValueChanged<FileVersion> onPreview;
  final ValueChanged<FileVersion> onDownload;
  final ValueChanged<FileVersion> onRestore;

  @override
  Widget build(BuildContext context) {
    final current = version.isCurrent;
    final semanticKind = current ? '当前版本' : '历史版本';
    final date = _formatDateTime(version.timestamp);
    final size = _formatBytes(version.size);
    final shortHash = _shortHash(version.hash);
    final restoreAllowed =
        canRestore && !current && version.hash != currentHash;

    return MnemoCard(
      key: Key('version-history-item-${version.sequence}-${version.hash}'),
      tone: current ? MnemoCardTone.brandTint : MnemoCardTone.elevated,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: <Widget>[
          Semantics(
            container: true,
            label: '状态：$semanticKind，保存于 $date，大小 $size，版本 ID $shortHash',
            child: ExcludeSemantics(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: <Widget>[
                  Row(
                    children: <Widget>[
                      DecoratedBox(
                        decoration: BoxDecoration(
                          color: Theme.of(context).colorScheme.primaryContainer,
                          borderRadius: BorderRadius.circular(MnemoRadius.sm),
                        ),
                        child: Padding(
                          padding: const EdgeInsets.all(MnemoSpacing.xs),
                          child: Icon(
                            current
                                ? Icons.description_rounded
                                : Icons.history_rounded,
                            color: Theme.of(
                              context,
                            ).colorScheme.onPrimaryContainer,
                          ),
                        ),
                      ),
                      const SizedBox(width: MnemoSpacing.sm),
                      Expanded(
                        child: Text(
                          semanticKind,
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                      ),
                      if (current)
                        const MnemoStatusPill(
                          label: '当前版本',
                          tone: MnemoStatusTone.success,
                          icon: Icons.check_rounded,
                          compact: true,
                        ),
                    ],
                  ),
                  const SizedBox(height: MnemoSpacing.sm),
                  Wrap(
                    spacing: MnemoSpacing.md,
                    runSpacing: MnemoSpacing.xs,
                    children: <Widget>[
                      _VersionMetadata(
                        icon: Icons.schedule_rounded,
                        label: date,
                      ),
                      _VersionMetadata(
                        icon: Icons.data_usage_rounded,
                        label: size,
                      ),
                      _VersionMetadata(
                        icon: Icons.fingerprint_rounded,
                        label: shortHash,
                        monospace: true,
                      ),
                    ],
                  ),
                  if (_displayComment(version.comment) case final comment?) ...[
                    const SizedBox(height: MnemoSpacing.xs),
                    Text(
                      comment,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ),
          if (!current) ...<Widget>[
            const SizedBox(height: MnemoSpacing.md),
            Wrap(
              alignment: WrapAlignment.end,
              spacing: MnemoSpacing.xs,
              runSpacing: MnemoSpacing.xs,
              children: <Widget>[
                OutlinedButton.icon(
                  key: Key(
                    'version-preview-${version.sequence}-${version.hash}',
                  ),
                  onPressed: actionsEnabled ? () => onPreview(version) : null,
                  icon: _ActionIcon(
                    pending:
                        pendingActionKey ==
                        'preview:${version.sequence}:${version.hash}',
                    label: '正在预览版本',
                    icon: Icons.visibility_outlined,
                  ),
                  label: const Text('预览'),
                ),
                OutlinedButton.icon(
                  key: Key(
                    'version-download-${version.sequence}-${version.hash}',
                  ),
                  onPressed: actionsEnabled ? () => onDownload(version) : null,
                  icon: _ActionIcon(
                    pending:
                        pendingActionKey ==
                        'download:${version.sequence}:${version.hash}',
                    label: '正在下载版本',
                    icon: Icons.download_rounded,
                  ),
                  label: const Text('下载'),
                ),
                if (restoreAllowed)
                  FilledButton.tonalIcon(
                    key: Key(
                      'version-restore-${version.sequence}-${version.hash}',
                    ),
                    onPressed: actionsEnabled ? () => onRestore(version) : null,
                    icon: const Icon(Icons.restore_rounded),
                    label: const Text('恢复'),
                  ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}

class _VersionMetadata extends StatelessWidget {
  const _VersionMetadata({
    required this.icon,
    required this.label,
    this.monospace = false,
  });

  final IconData icon;
  final String label;
  final bool monospace;

  @override
  Widget build(BuildContext context) {
    final color = Theme.of(context).colorScheme.onSurfaceVariant;
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: <Widget>[
        Icon(icon, size: 16, color: color),
        const SizedBox(width: MnemoSpacing.xxs),
        Text(
          label,
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
            color: color,
            fontFamily: monospace ? 'monospace' : null,
          ),
        ),
      ],
    );
  }
}

class _ActionIcon extends StatelessWidget {
  const _ActionIcon({
    required this.pending,
    required this.label,
    required this.icon,
  });

  final bool pending;
  final String label;
  final IconData icon;

  @override
  Widget build(BuildContext context) {
    if (!pending) {
      return Icon(icon);
    }
    return Semantics(
      liveRegion: true,
      label: label,
      child: const SizedBox.square(
        dimension: 18,
        child: CircularProgressIndicator(strokeWidth: 2),
      ),
    );
  }
}

final class _RestoreDialogResult {
  const _RestoreDialogResult.success(this.history) : requiresRefresh = false;

  const _RestoreDialogResult.requiresRefresh()
    : history = null,
      requiresRefresh = true;

  final FileVersionHistory? history;
  final bool requiresRefresh;
}

class _VersionRestoreDialog extends StatefulWidget {
  const _VersionRestoreDialog({
    required this.entry,
    required this.version,
    required this.onRestore,
    required this.errorMessageBuilder,
  });

  final FileEntry entry;
  final FileVersion version;
  final Future<FileVersionHistory> Function(FileVersion version) onRestore;
  final VersionHistoryErrorMessageBuilder? errorMessageBuilder;

  @override
  State<_VersionRestoreDialog> createState() => _VersionRestoreDialogState();
}

class _VersionRestoreDialogState extends State<_VersionRestoreDialog> {
  bool _submitted = false;
  bool _submitting = false;
  bool _requiresRefreshOnClose = false;
  String? _errorMessage;

  Future<void> _submit() async {
    if (_submitted) {
      return;
    }
    setState(() {
      _submitted = true;
      _submitting = true;
      _errorMessage = null;
    });
    try {
      final history = await widget.onRestore(widget.version);
      if (history.path != widget.entry.path) {
        throw const FormatException(
          'Restored version history belongs to a different file',
        );
      }
      if (mounted) {
        Navigator.pop(context, _RestoreDialogResult.success(history));
      }
    } on Object catch (error) {
      if (!mounted) {
        return;
      }
      final described = widget.errorMessageBuilder?.call(error).trim();
      final detail = described == null || described.isEmpty
          ? '恢复结果未确认。'
          : described;
      final requiresRefresh = _restoreFailureRequiresReconciliation(error);
      setState(() {
        _submitting = false;
        _requiresRefreshOnClose = requiresRefresh;
        _errorMessage = requiresRefresh
            ? '$detail 为避免重复提交，请关闭并刷新目录后重新打开版本历史。'
            : '$detail 本次恢复未完成。请关闭并刷新版本历史后重新确认。';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final version = widget.version;
    return PopScope(
      canPop: !_submitting && !_submitted,
      child: AlertDialog(
        icon: const Icon(Icons.restore_rounded),
        title: const Text('恢复此历史版本？'),
        content: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 520),
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: <Widget>[
                _RestoreDetail(label: '文件', value: widget.entry.path),
                const SizedBox(height: MnemoSpacing.xs),
                _RestoreDetail(
                  label: '保存时间',
                  value: _formatDateTime(version.timestamp),
                ),
                const SizedBox(height: MnemoSpacing.xs),
                _RestoreDetail(
                  label: '文件大小',
                  value: _formatBytes(version.size),
                ),
                const SizedBox(height: MnemoSpacing.xs),
                _RestoreDetail(
                  label: '版本 ID',
                  value: _shortHash(version.hash),
                  monospace: true,
                ),
                const SizedBox(height: MnemoSpacing.md),
                MnemoCard(
                  tone: MnemoCardTone.brandTint,
                  child: Text(
                    '恢复会用所选历史内容覆盖当前文件。服务端会先把当前内容保存为新的安全版本；'
                    '若权限、配额、路径状态或版本校验不通过，恢复会被拒绝。',
                    style: Theme.of(context).textTheme.bodyMedium,
                  ),
                ),
                if (_submitting) ...<Widget>[
                  const SizedBox(height: MnemoSpacing.md),
                  Semantics(
                    liveRegion: true,
                    label: '正在恢复历史版本',
                    child: const LinearProgressIndicator(),
                  ),
                ],
                if (_errorMessage case final message?) ...<Widget>[
                  const SizedBox(height: MnemoSpacing.md),
                  MnemoErrorNotice(title: '需要核对恢复结果', message: message),
                ],
              ],
            ),
          ),
        ),
        actions: <Widget>[
          TextButton(
            key: const Key('version-restore-cancel'),
            onPressed: _submitting
                ? null
                : () => Navigator.pop(
                    context,
                    _submitted && _requiresRefreshOnClose
                        ? const _RestoreDialogResult.requiresRefresh()
                        : null,
                  ),
            child: Text(_submitted ? '关闭' : '取消'),
          ),
          if (!_submitted)
            FilledButton(
              key: const Key('version-restore-confirm'),
              onPressed: _submit,
              child: const Text('确认恢复'),
            ),
        ],
      ),
    );
  }
}

bool _restoreFailureRequiresReconciliation(Object error) {
  if (error is! ApiException) {
    return true;
  }
  return error.code == 'VERSION_RESTORE_RESULT_UNCONFIRMED' ||
      error.code == 'VERSION_RESTORE_CONFIRMED_REFRESH_REQUIRED';
}

class _RestoreDetail extends StatelessWidget {
  const _RestoreDetail({
    required this.label,
    required this.value,
    this.monospace = false,
  });

  final String label;
  final String value;
  final bool monospace;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: <Widget>[
        SizedBox(
          width: 72,
          child: Text(
            label,
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ),
        Expanded(
          child: Text(
            value,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              fontFamily: monospace ? 'monospace' : null,
            ),
          ),
        ),
      ],
    );
  }
}

String? _displayComment(String? comment) {
  return switch (comment) {
    null || '' || '(current)' => null,
    'before restore' => '恢复前自动保留',
    final value => value,
  };
}

String _shortHash(String hash) {
  return hash.length <= 12 ? hash : '${hash.substring(0, 12)}…';
}

String _formatDateTime(DateTime value) {
  return DateFormat('yyyy-MM-dd HH:mm').format(value.toLocal());
}

String _formatBytes(int bytes) {
  const units = <String>['B', 'KB', 'MB', 'GB', 'TB'];
  var value = bytes.toDouble();
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final precision = unit == 0 || value >= 10 ? 0 : 1;
  return '${value.toStringAsFixed(precision)} ${units[unit]}';
}
