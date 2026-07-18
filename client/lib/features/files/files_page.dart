import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../core/files/file_models.dart';
import '../../design_system/design_system.dart';

enum FilesLoadState { loading, ready, error }

enum FilesDisplayMode { list, grid }

enum FilesAddAction { createFolder, uploadFiles }

enum FileItemAction { download, rename, move, copy, delete, details }

final class FileActionRequest {
  const FileActionRequest({required this.action, required this.entries});

  final FileItemAction action;
  final List<FileEntry> entries;
}

final class FilesViewModel {
  const FilesViewModel._({
    required this.state,
    required this.path,
    this.entries = const <FileEntry>[],
    this.canWrite = false,
    this.errorMessage,
  });

  const FilesViewModel.loading({String path = '/'})
    : this._(state: FilesLoadState.loading, path: path);

  const FilesViewModel.ready({
    required String path,
    required List<FileEntry> entries,
    required bool canWrite,
  }) : this._(
         state: FilesLoadState.ready,
         path: path,
         entries: entries,
         canWrite: canWrite,
       );

  const FilesViewModel.error({required String path, required String message})
    : this._(state: FilesLoadState.error, path: path, errorMessage: message);

  factory FilesViewModel.fromListing(DirectoryListing listing) {
    return FilesViewModel.ready(
      path: listing.path,
      entries: listing.entries,
      canWrite: listing.capabilities.write,
    );
  }

  final FilesLoadState state;
  final String path;
  final List<FileEntry> entries;
  final bool canWrite;
  final String? errorMessage;
}

class FilesPage extends StatefulWidget {
  const FilesPage({
    super.key,
    required this.viewModel,
    required this.onRefresh,
    required this.onNavigatePath,
    required this.onOpenFile,
    required this.onAddAction,
    required this.onFileAction,
    this.initialDisplayMode = FilesDisplayMode.list,
  });

  final FilesViewModel viewModel;
  final Future<void> Function() onRefresh;
  final ValueChanged<String> onNavigatePath;
  final ValueChanged<FileEntry> onOpenFile;
  final ValueChanged<FilesAddAction> onAddAction;
  final ValueChanged<FileActionRequest> onFileAction;
  final FilesDisplayMode initialDisplayMode;

  @override
  State<FilesPage> createState() => _FilesPageState();
}

class _FilesPageState extends State<FilesPage> {
  late FilesDisplayMode _displayMode = widget.initialDisplayMode;
  final Set<String> _selectedPaths = <String>{};

  @override
  void didUpdateWidget(covariant FilesPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.viewModel.path != widget.viewModel.path) {
      _selectedPaths.clear();
      return;
    }
    final Set<String> available = widget.viewModel.entries
        .map((FileEntry entry) => entry.path)
        .toSet();
    _selectedPaths.removeWhere((String path) => !available.contains(path));
  }

  List<FileEntry> get _selectedEntries => widget.viewModel.entries
      .where((FileEntry entry) => _selectedPaths.contains(entry.path))
      .toList(growable: false);

  void _toggleSelection(FileEntry entry) {
    unawaited(HapticFeedback.selectionClick());
    setState(() {
      if (!_selectedPaths.add(entry.path)) {
        _selectedPaths.remove(entry.path);
      }
    });
  }

  void _clearSelection() => setState(_selectedPaths.clear);

  void _activate(FileEntry entry) {
    if (_selectedPaths.isNotEmpty) {
      _toggleSelection(entry);
      return;
    }
    if (entry.isDirectory) {
      widget.onNavigatePath(entry.path);
    } else {
      widget.onOpenFile(entry);
    }
  }

  void _dispatchAction(FileItemAction action, List<FileEntry> entries) {
    widget.onFileAction(
      FileActionRequest(
        action: action,
        entries: List<FileEntry>.unmodifiable(entries),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: <Widget>[
        _FilesToolbar(
          path: widget.viewModel.path,
          displayMode: _displayMode,
          selectedEntries: _selectedEntries,
          canWrite: widget.viewModel.canWrite,
          onNavigatePath: (String path) {
            _clearSelection();
            widget.onNavigatePath(path);
          },
          onDisplayModeChanged: (FilesDisplayMode mode) =>
              setState(() => _displayMode = mode),
          onClearSelection: _clearSelection,
          onAddAction: widget.onAddAction,
          onSelectionAction: (FileItemAction action) {
            final entries = _selectedEntries;
            _dispatchAction(action, entries);
          },
        ),
        Expanded(child: _buildContent()),
      ],
    );
  }

  Widget _buildContent() {
    return switch (widget.viewModel.state) {
      FilesLoadState.loading => const _FilesLoading(),
      FilesLoadState.error => _FilesError(
        message: widget.viewModel.errorMessage ?? '文件加载失败',
        onRetry: widget.onRefresh,
      ),
      FilesLoadState.ready => _FilesReady(
        entries: widget.viewModel.entries,
        canWrite: widget.viewModel.canWrite,
        displayMode: _displayMode,
        selectedPaths: _selectedPaths,
        onRefresh: widget.onRefresh,
        onActivate: _activate,
        onLongPress: _toggleSelection,
        onItemAction: (FileEntry entry, FileItemAction action) =>
            _dispatchAction(action, <FileEntry>[entry]),
        onAddAction: widget.onAddAction,
      ),
    };
  }
}

class _FilesToolbar extends StatelessWidget {
  const _FilesToolbar({
    required this.path,
    required this.displayMode,
    required this.selectedEntries,
    required this.canWrite,
    required this.onNavigatePath,
    required this.onDisplayModeChanged,
    required this.onClearSelection,
    required this.onAddAction,
    required this.onSelectionAction,
  });

  final String path;
  final FilesDisplayMode displayMode;
  final List<FileEntry> selectedEntries;
  final bool canWrite;
  final ValueChanged<String> onNavigatePath;
  final ValueChanged<FilesDisplayMode> onDisplayModeChanged;
  final VoidCallback onClearSelection;
  final ValueChanged<FilesAddAction> onAddAction;
  final ValueChanged<FileItemAction> onSelectionAction;

  @override
  Widget build(BuildContext context) {
    final bool selecting = selectedEntries.isNotEmpty;
    final bool canMutateSelected =
        selecting &&
        selectedEntries.every((FileEntry entry) => entry.capabilities.write);
    final bool canDownloadSelected =
        selectedEntries.length == 1 &&
        !selectedEntries.single.isDirectory &&
        selectedEntries.single.capabilities.concreteRead;
    return Material(
      color: Theme.of(context).colorScheme.surface,
      child: SafeArea(
        bottom: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(
            MnemoSpacing.md,
            MnemoSpacing.xs,
            MnemoSpacing.xs,
            MnemoSpacing.xs,
          ),
          child: selecting
              ? Row(
                  children: <Widget>[
                    IconButton(
                      key: const Key('files-clear-selection'),
                      onPressed: onClearSelection,
                      tooltip: '取消选择',
                      icon: const Icon(Icons.close_rounded),
                    ),
                    Expanded(
                      child: Text(
                        '已选择 ${selectedEntries.length} 项',
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                    ),
                    if (canDownloadSelected)
                      IconButton(
                        onPressed: () =>
                            onSelectionAction(FileItemAction.download),
                        tooltip: '下载',
                        icon: const Icon(Icons.download_rounded),
                      ),
                    if (canMutateSelected)
                      IconButton(
                        onPressed: () => onSelectionAction(FileItemAction.move),
                        tooltip: '移动',
                        icon: const Icon(Icons.drive_file_move_outline),
                      ),
                    if (canMutateSelected)
                      IconButton(
                        key: const Key('files-delete-selection'),
                        onPressed: () =>
                            onSelectionAction(FileItemAction.delete),
                        tooltip: '删除',
                        icon: const Icon(Icons.delete_outline_rounded),
                      ),
                  ],
                )
              : Row(
                  children: <Widget>[
                    Expanded(
                      child: _Breadcrumbs(
                        path: path,
                        onNavigatePath: onNavigatePath,
                      ),
                    ),
                    _DisplayModeButton(
                      displayMode: displayMode,
                      onChanged: onDisplayModeChanged,
                    ),
                    if (canWrite)
                      PopupMenuButton<FilesAddAction>(
                        key: const Key('files-add-menu'),
                        tooltip: '添加',
                        icon: const Icon(Icons.add_rounded),
                        onSelected: onAddAction,
                        itemBuilder: (BuildContext context) =>
                            const <PopupMenuEntry<FilesAddAction>>[
                              PopupMenuItem<FilesAddAction>(
                                value: FilesAddAction.createFolder,
                                child: ListTile(
                                  leading: Icon(
                                    Icons.create_new_folder_outlined,
                                  ),
                                  title: Text('新建文件夹'),
                                  contentPadding: EdgeInsets.zero,
                                ),
                              ),
                              PopupMenuItem<FilesAddAction>(
                                value: FilesAddAction.uploadFiles,
                                child: ListTile(
                                  leading: Icon(Icons.upload_file_outlined),
                                  title: Text('上传文件'),
                                  contentPadding: EdgeInsets.zero,
                                ),
                              ),
                            ],
                      ),
                  ],
                ),
        ),
      ),
    );
  }
}

class _Breadcrumbs extends StatelessWidget {
  const _Breadcrumbs({required this.path, required this.onNavigatePath});

  final String path;
  final ValueChanged<String> onNavigatePath;

  @override
  Widget build(BuildContext context) {
    final List<String> segments = path
        .split('/')
        .where((String segment) => segment.isNotEmpty)
        .toList(growable: false);
    final List<({String label, String path})> crumbs =
        <({String label, String path})>[
          (label: '我的文件', path: '/'),
          for (var index = 0; index < segments.length; index++)
            (
              label: segments[index],
              path: '/${segments.take(index + 1).join('/')}',
            ),
        ];

    return SizedBox(
      height: MnemoControlSize.minimumTouchTarget,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        itemCount: crumbs.length,
        separatorBuilder: (_, _) =>
            const Icon(Icons.chevron_right_rounded, size: 18),
        itemBuilder: (BuildContext context, int index) {
          final crumb = crumbs[index];
          final bool current = index == crumbs.length - 1;
          return TextButton(
            key: Key('files-breadcrumb-${crumb.path}'),
            onPressed: current ? null : () => onNavigatePath(crumb.path),
            style: TextButton.styleFrom(
              padding: const EdgeInsets.symmetric(horizontal: MnemoSpacing.xs),
              disabledForegroundColor: Theme.of(context).colorScheme.onSurface,
            ),
            child: Text(
              crumb.label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          );
        },
      ),
    );
  }
}

class _DisplayModeButton extends StatelessWidget {
  const _DisplayModeButton({
    required this.displayMode,
    required this.onChanged,
  });

  final FilesDisplayMode displayMode;
  final ValueChanged<FilesDisplayMode> onChanged;

  @override
  Widget build(BuildContext context) {
    final bool list = displayMode == FilesDisplayMode.list;
    return IconButton(
      key: const Key('files-display-mode'),
      onPressed: () =>
          onChanged(list ? FilesDisplayMode.grid : FilesDisplayMode.list),
      tooltip: list ? '切换到网格视图' : '切换到列表视图',
      icon: Icon(list ? Icons.grid_view_rounded : Icons.view_list_rounded),
    );
  }
}

class _FilesReady extends StatelessWidget {
  const _FilesReady({
    required this.entries,
    required this.canWrite,
    required this.displayMode,
    required this.selectedPaths,
    required this.onRefresh,
    required this.onActivate,
    required this.onLongPress,
    required this.onItemAction,
    required this.onAddAction,
  });

  final List<FileEntry> entries;
  final bool canWrite;
  final FilesDisplayMode displayMode;
  final Set<String> selectedPaths;
  final Future<void> Function() onRefresh;
  final ValueChanged<FileEntry> onActivate;
  final ValueChanged<FileEntry> onLongPress;
  final void Function(FileEntry, FileItemAction) onItemAction;
  final ValueChanged<FilesAddAction> onAddAction;

  @override
  Widget build(BuildContext context) {
    if (entries.isEmpty) {
      return RefreshIndicator(
        onRefresh: onRefresh,
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: <Widget>[
            SizedBox(
              height: MediaQuery.sizeOf(context).height * 0.58,
              child: MnemoEmptyState(
                title: '这里还没有文件',
                message: canWrite ? '可以新建文件夹或从当前设备上传文件。' : '当前账户没有写入此目录的权限。',
                primaryAction: canWrite
                    ? FilledButton.icon(
                        onPressed: () =>
                            onAddAction(FilesAddAction.uploadFiles),
                        icon: const Icon(Icons.upload_file_outlined),
                        label: const Text('上传文件'),
                      )
                    : null,
                secondaryAction: canWrite
                    ? OutlinedButton.icon(
                        onPressed: () =>
                            onAddAction(FilesAddAction.createFolder),
                        icon: const Icon(Icons.create_new_folder_outlined),
                        label: const Text('新建文件夹'),
                      )
                    : null,
              ),
            ),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: onRefresh,
      child: displayMode == FilesDisplayMode.list
          ? ListView.separated(
              key: const Key('files-list'),
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.fromLTRB(
                MnemoSpacing.sm,
                MnemoSpacing.xs,
                MnemoSpacing.sm,
                MnemoSpacing.xxl,
              ),
              itemCount: entries.length,
              separatorBuilder: (_, _) => const SizedBox(height: 2),
              itemBuilder: (BuildContext context, int index) {
                final entry = entries[index];
                return _FileListTile(
                  entry: entry,
                  selected: selectedPaths.contains(entry.path),
                  selectionMode: selectedPaths.isNotEmpty,
                  onTap: () => onActivate(entry),
                  onLongPress: () => onLongPress(entry),
                  onAction: (FileItemAction action) =>
                      onItemAction(entry, action),
                );
              },
            )
          : GridView.builder(
              key: const Key('files-grid'),
              physics: const AlwaysScrollableScrollPhysics(),
              padding: const EdgeInsets.all(MnemoSpacing.md),
              gridDelegate: SliverGridDelegateWithMaxCrossAxisExtent(
                maxCrossAxisExtent: 180,
                mainAxisExtent: 176,
                crossAxisSpacing: MnemoSpacing.sm,
                mainAxisSpacing: MnemoSpacing.sm,
              ),
              itemCount: entries.length,
              itemBuilder: (BuildContext context, int index) {
                final entry = entries[index];
                return _FileGridTile(
                  entry: entry,
                  selected: selectedPaths.contains(entry.path),
                  selectionMode: selectedPaths.isNotEmpty,
                  onTap: () => onActivate(entry),
                  onLongPress: () => onLongPress(entry),
                  onAction: (FileItemAction action) =>
                      onItemAction(entry, action),
                );
              },
            ),
    );
  }
}

class _FileListTile extends StatelessWidget {
  const _FileListTile({
    required this.entry,
    required this.selected,
    required this.selectionMode,
    required this.onTap,
    required this.onLongPress,
    required this.onAction,
  });

  final FileEntry entry;
  final bool selected;
  final bool selectionMode;
  final VoidCallback onTap;
  final VoidCallback onLongPress;
  final ValueChanged<FileItemAction> onAction;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    return Semantics(
      selected: selected,
      button: true,
      label: '${entry.isDirectory ? '文件夹' : '文件'} ${entry.name}',
      child: Material(
        color: selected ? colors.primaryContainer : Colors.transparent,
        borderRadius: BorderRadius.circular(MnemoRadius.sm),
        clipBehavior: Clip.antiAlias,
        child: ListTile(
          key: Key('file-item-${entry.path}'),
          onTap: onTap,
          onLongPress: onLongPress,
          leading: selectionMode
              ? Checkbox(value: selected, onChanged: (_) => onTap())
              : _FileIcon(entry: entry, size: 42),
          title: Text(entry.name, maxLines: 1, overflow: TextOverflow.ellipsis),
          subtitle: Text(
            entry.isDirectory
                ? _formatModifiedAt(entry.modifiedAt)
                : '${_formatFileSize(entry.size)} · ${_formatModifiedAt(entry.modifiedAt)}',
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          trailing: selectionMode
              ? null
              : _FileActionsMenu(entry: entry, onSelected: onAction),
        ),
      ),
    );
  }
}

class _FileGridTile extends StatelessWidget {
  const _FileGridTile({
    required this.entry,
    required this.selected,
    required this.selectionMode,
    required this.onTap,
    required this.onLongPress,
    required this.onAction,
  });

  final FileEntry entry;
  final bool selected;
  final bool selectionMode;
  final VoidCallback onTap;
  final VoidCallback onLongPress;
  final ValueChanged<FileItemAction> onAction;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    return Semantics(
      selected: selected,
      button: true,
      label: '${entry.isDirectory ? '文件夹' : '文件'} ${entry.name}',
      child: Material(
        key: Key('file-item-${entry.path}'),
        color: selected ? colors.primaryContainer : colors.surface,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(MnemoRadius.md),
          side: BorderSide(
            color: selected ? colors.primary : colors.outlineVariant,
          ),
        ),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          onLongPress: onLongPress,
          child: Padding(
            padding: const EdgeInsets.all(MnemoSpacing.sm),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: <Widget>[
                Row(
                  children: <Widget>[
                    _FileIcon(entry: entry, size: 52),
                    const Spacer(),
                    if (selectionMode)
                      Checkbox(value: selected, onChanged: (_) => onTap())
                    else
                      _FileActionsMenu(
                        entry: entry,
                        onSelected: onAction,
                        compact: true,
                      ),
                  ],
                ),
                const Spacer(),
                Text(
                  entry.name,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.titleSmall,
                ),
                const SizedBox(height: MnemoSpacing.xxs),
                Text(
                  entry.isDirectory ? '文件夹' : _formatFileSize(entry.size),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: colors.onSurfaceVariant,
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

class _FileActionsMenu extends StatelessWidget {
  const _FileActionsMenu({
    required this.entry,
    required this.onSelected,
    this.compact = false,
  });

  final FileEntry entry;
  final ValueChanged<FileItemAction> onSelected;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<FileItemAction>(
      tooltip: '文件操作',
      padding: EdgeInsets.zero,
      constraints: compact
          ? const BoxConstraints.tightFor(width: 40, height: 40)
          : null,
      icon: const Icon(Icons.more_vert_rounded),
      onSelected: onSelected,
      itemBuilder: (BuildContext context) => <PopupMenuEntry<FileItemAction>>[
        if (!entry.isDirectory && entry.capabilities.concreteRead)
          const PopupMenuItem<FileItemAction>(
            value: FileItemAction.download,
            child: _MenuLabel(icon: Icons.download_rounded, label: '下载'),
          ),
        if (entry.capabilities.write) ...<PopupMenuEntry<FileItemAction>>[
          const PopupMenuItem<FileItemAction>(
            value: FileItemAction.rename,
            child: _MenuLabel(icon: Icons.edit_outlined, label: '重命名'),
          ),
          const PopupMenuItem<FileItemAction>(
            value: FileItemAction.move,
            child: _MenuLabel(icon: Icons.drive_file_move_outline, label: '移动'),
          ),
          const PopupMenuItem<FileItemAction>(
            value: FileItemAction.copy,
            child: _MenuLabel(icon: Icons.copy_outlined, label: '复制'),
          ),
          const PopupMenuDivider(),
          const PopupMenuItem<FileItemAction>(
            value: FileItemAction.delete,
            child: _MenuLabel(icon: Icons.delete_outline_rounded, label: '删除'),
          ),
        ],
        const PopupMenuItem<FileItemAction>(
          value: FileItemAction.details,
          child: _MenuLabel(icon: Icons.info_outline_rounded, label: '详细信息'),
        ),
      ],
    );
  }
}

class _MenuLabel extends StatelessWidget {
  const _MenuLabel({required this.icon, required this.label});

  final IconData icon;
  final String label;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: <Widget>[
        Icon(icon, size: 20),
        const SizedBox(width: MnemoSpacing.sm),
        Text(label),
      ],
    );
  }
}

class _FileIcon extends StatelessWidget {
  const _FileIcon({required this.entry, required this.size});

  final FileEntry entry;
  final double size;

  @override
  Widget build(BuildContext context) {
    final (IconData, Color) visual = _fileVisual(entry, context);
    return DecoratedBox(
      decoration: BoxDecoration(
        color: visual.$2.withValues(alpha: 0.13),
        borderRadius: BorderRadius.circular(MnemoRadius.sm),
      ),
      child: SizedBox.square(
        dimension: size,
        child: Icon(visual.$1, size: size * 0.54, color: visual.$2),
      ),
    );
  }
}

class _FilesLoading extends StatelessWidget {
  const _FilesLoading();

  @override
  Widget build(BuildContext context) {
    return const MnemoContentFrame(
      useSafeArea: false,
      child: MnemoSkeletonList(itemCount: 6),
    );
  }
}

class _FilesError extends StatelessWidget {
  const _FilesError({required this.message, required this.onRetry});

  final String message;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return MnemoContentFrame(
      useSafeArea: false,
      alignment: Alignment.center,
      child: MnemoErrorNotice(
        title: '文件加载失败',
        message: message,
        onRetry: onRetry,
      ),
    );
  }
}

(IconData, Color) _fileVisual(FileEntry entry, BuildContext context) {
  final ColorScheme colors = Theme.of(context).colorScheme;
  if (entry.isDirectory) {
    return (Icons.folder_rounded, colors.primary);
  }
  final String extension = entry.name.contains('.')
      ? entry.name.split('.').last.toLowerCase()
      : '';
  if (<String>{
    'jpg',
    'jpeg',
    'png',
    'gif',
    'webp',
    'heic',
  }.contains(extension)) {
    return (Icons.image_outlined, MnemoPalette.rose);
  }
  if (<String>{'mp4', 'mov', 'mkv', 'avi', 'webm'}.contains(extension)) {
    return (Icons.movie_outlined, MnemoPalette.aurora);
  }
  if (<String>{'mp3', 'flac', 'wav', 'm4a', 'aac'}.contains(extension)) {
    return (Icons.audio_file_outlined, MnemoPalette.starlight);
  }
  if (extension == 'pdf') {
    return (Icons.picture_as_pdf_outlined, colors.error);
  }
  if (<String>{'zip', 'tar', 'gz', '7z', 'rar'}.contains(extension)) {
    return (Icons.archive_outlined, colors.tertiary);
  }
  return (Icons.insert_drive_file_outlined, colors.onSurfaceVariant);
}

String _formatFileSize(int bytes) {
  if (bytes < 1024) {
    return '$bytes B';
  }
  const units = <String>['KB', 'MB', 'GB', 'TB', 'PB'];
  double value = bytes / 1024;
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final digits = value >= 100
      ? 0
      : value >= 10
      ? 1
      : 2;
  return '${value.toStringAsFixed(digits)} ${units[unit]}';
}

String _formatModifiedAt(DateTime value) {
  final DateTime local = value.toLocal();
  final DateTime now = DateTime.now();
  if (local.year == now.year &&
      local.month == now.month &&
      local.day == now.day) {
    return '今天 ${_twoDigits(local.hour)}:${_twoDigits(local.minute)}';
  }
  if (local.year == now.year) {
    return '${local.month}月${local.day}日';
  }
  return '${local.year}年${local.month}月${local.day}日';
}

String _twoDigits(int value) => value.toString().padLeft(2, '0');
