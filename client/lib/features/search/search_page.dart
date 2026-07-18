import 'dart:async';

import 'package:flutter/material.dart';

import '../../core/search/search_models.dart';
import '../../design_system/design_system.dart';

enum SearchLoadState { idle, loading, ready, error }

final class SearchViewModel {
  const SearchViewModel._({
    required this.state,
    required this.query,
    this.listing,
    this.refreshing = false,
    this.errorMessage,
    this.refreshErrorMessage,
  });

  const SearchViewModel.idle({String query = ''})
    : this._(state: SearchLoadState.idle, query: query);

  const SearchViewModel.loading(String query)
    : this._(state: SearchLoadState.loading, query: query);

  SearchViewModel.ready({
    required SearchListing listing,
    bool refreshing = false,
    String? refreshErrorMessage,
  }) : this._(
         state: SearchLoadState.ready,
         query: listing.query,
         listing: listing,
         refreshing: refreshing,
         refreshErrorMessage: refreshErrorMessage,
       );

  const SearchViewModel.error({required String query, required String message})
    : this._(state: SearchLoadState.error, query: query, errorMessage: message);

  final SearchLoadState state;
  final String query;
  final SearchListing? listing;
  final bool refreshing;
  final String? errorMessage;
  final String? refreshErrorMessage;
}

class SearchPage extends StatefulWidget {
  const SearchPage({
    super.key,
    required this.viewModel,
    required this.onSearch,
    required this.onClear,
    required this.onClose,
    required this.onOpenResult,
  });

  final SearchViewModel viewModel;
  final Future<void> Function(String query) onSearch;
  final VoidCallback onClear;
  final VoidCallback onClose;
  final Future<void> Function(SearchResultItem result) onOpenResult;

  @override
  State<SearchPage> createState() => _SearchPageState();
}

class _SearchPageState extends State<SearchPage> {
  static const Duration _debounceDuration = Duration(milliseconds: 350);

  late final TextEditingController _queryController = TextEditingController(
    text: widget.viewModel.query,
  );
  final FocusNode _queryFocus = FocusNode();
  Timer? _debounce;
  String? _validationMessage;
  String? _openingPath;

  @override
  void didUpdateWidget(covariant SearchPage oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.viewModel.query == widget.viewModel.query ||
        _queryController.text == widget.viewModel.query) {
      return;
    }
    if (!_queryFocus.hasFocus ||
        _queryController.text == oldWidget.viewModel.query) {
      _queryController.value = TextEditingValue(
        text: widget.viewModel.query,
        selection: TextSelection.collapsed(
          offset: widget.viewModel.query.length,
        ),
      );
    }
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _queryController.dispose();
    _queryFocus.dispose();
    super.dispose();
  }

  void _onQueryChanged(String input) {
    _debounce?.cancel();
    final String? validationMessage = _validateQuery(input);
    setState(() => _validationMessage = validationMessage);
    if (input.trim().isEmpty || validationMessage != null) {
      widget.onClear();
      return;
    }
    final String query = normalizeSearchQuery(input);
    if (query != widget.viewModel.query) {
      widget.onClear();
    }
    _debounce = Timer(
      _debounceDuration,
      () => unawaited(_performSearch(query)),
    );
  }

  void _onSubmitted(String input) {
    _debounce?.cancel();
    final String? validationMessage = _validateQuery(input);
    setState(() => _validationMessage = validationMessage);
    if (input.trim().isEmpty || validationMessage != null) {
      widget.onClear();
      return;
    }
    unawaited(_performSearch(normalizeSearchQuery(input)));
  }

  String? _validateQuery(String input) {
    if (input.trim().isEmpty) {
      return null;
    }
    try {
      normalizeSearchQuery(input);
      return null;
    } on FormatException catch (error) {
      return switch (error.message) {
        'Search query is too long' => '搜索内容不能超过 100 个字符',
        'Search query must not contain control characters' => '搜索内容不能包含控制字符',
        _ => '请输入有效的文件名',
      };
    }
  }

  Future<void> _performSearch(String query) async {
    try {
      await widget.onSearch(query);
    } on Object {
      // Search failures are represented by SearchViewModel.
    }
  }

  Future<void> _refresh() {
    final String query = widget.viewModel.query;
    if (query.isEmpty) {
      return Future<void>.value();
    }
    return _performSearch(query);
  }

  void _clear() {
    _debounce?.cancel();
    _queryController.clear();
    setState(() {
      _validationMessage = null;
      _openingPath = null;
    });
    widget.onClear();
    _queryFocus.requestFocus();
  }

  void _close() {
    _debounce?.cancel();
    _queryFocus.unfocus();
    widget.onClose();
  }

  Future<void> _openResult(SearchResultItem result) async {
    if (_openingPath != null) {
      return;
    }
    setState(() => _openingPath = result.path);
    try {
      await widget.onOpenResult(result);
    } on Object {
      // Opening errors are surfaced by the application shell.
    } finally {
      if (mounted) {
        setState(() => _openingPath = null);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final bool busy =
        widget.viewModel.state == SearchLoadState.loading ||
        widget.viewModel.refreshing;
    return PopScope(
      canPop: false,
      onPopInvokedWithResult: (bool didPop, Object? result) {
        if (!didPop) {
          _close();
        }
      },
      child: Column(
        children: <Widget>[
          _SearchToolbar(
            controller: _queryController,
            focusNode: _queryFocus,
            validationMessage: _validationMessage,
            busy: busy,
            onChanged: _onQueryChanged,
            onSubmitted: _onSubmitted,
            onClear: _clear,
            onClose: _close,
          ),
          Expanded(child: _buildContent()),
        ],
      ),
    );
  }

  Widget _buildContent() {
    if (_validationMessage case final String message?) {
      return _SearchFeedback(
        key: const Key('search-validation'),
        title: '无法搜索',
        message: message,
        icon: Icons.search_off_rounded,
      );
    }
    return switch (widget.viewModel.state) {
      SearchLoadState.idle => const _SearchFeedback(
        key: Key('search-idle'),
        title: '搜索文件',
        message: '输入文件名即可查找当前账户可访问的文件和文件夹。',
        icon: Icons.manage_search_rounded,
      ),
      SearchLoadState.loading => const _SearchLoading(),
      SearchLoadState.error => _SearchError(
        message: widget.viewModel.errorMessage ?? '暂时无法完成搜索。',
        onRetry: () => _performSearch(widget.viewModel.query),
      ),
      SearchLoadState.ready => _SearchReady(
        listing: widget.viewModel.listing!,
        refreshing: widget.viewModel.refreshing,
        refreshErrorMessage: widget.viewModel.refreshErrorMessage,
        openingPath: _openingPath,
        onRefresh: _refresh,
        onOpenResult: _openResult,
      ),
    };
  }
}

class _SearchToolbar extends StatelessWidget {
  const _SearchToolbar({
    required this.controller,
    required this.focusNode,
    required this.validationMessage,
    required this.busy,
    required this.onChanged,
    required this.onSubmitted,
    required this.onClear,
    required this.onClose,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final String? validationMessage;
  final bool busy;
  final ValueChanged<String> onChanged;
  final ValueChanged<String> onSubmitted;
  final VoidCallback onClear;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    return Material(
      color: colors.surface,
      child: Column(
        children: <Widget>[
          MnemoContentFrame(
            maxWidth: 920,
            useSafeArea: false,
            padding: const EdgeInsets.fromLTRB(
              MnemoSpacing.xs,
              MnemoSpacing.xs,
              MnemoSpacing.md,
              MnemoSpacing.xs,
            ),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: <Widget>[
                IconButton(
                  key: const Key('search-close'),
                  onPressed: onClose,
                  tooltip: '返回',
                  icon: const Icon(Icons.arrow_back_rounded),
                ),
                const SizedBox(width: MnemoSpacing.xs),
                Expanded(
                  child: ValueListenableBuilder<TextEditingValue>(
                    valueListenable: controller,
                    builder:
                        (
                          BuildContext context,
                          TextEditingValue value,
                          Widget? child,
                        ) {
                          return TextField(
                            key: const Key('search-query-input'),
                            controller: controller,
                            focusNode: focusNode,
                            autofocus: true,
                            textInputAction: TextInputAction.search,
                            autocorrect: false,
                            enableSuggestions: false,
                            decoration: InputDecoration(
                              hintText: '搜索文件名',
                              errorText: validationMessage,
                              prefixIcon: const Icon(Icons.search_rounded),
                              suffixIcon: value.text.isEmpty
                                  ? null
                                  : IconButton(
                                      key: const Key('search-clear'),
                                      onPressed: onClear,
                                      tooltip: '清除搜索内容',
                                      icon: const Icon(Icons.close_rounded),
                                    ),
                            ),
                            onChanged: onChanged,
                            onSubmitted: onSubmitted,
                          );
                        },
                  ),
                ),
              ],
            ),
          ),
          if (busy)
            const LinearProgressIndicator(
              key: Key('search-progress'),
              minHeight: 2,
            )
          else
            Divider(height: 1, color: colors.outlineVariant),
        ],
      ),
    );
  }
}

class _SearchLoading extends StatelessWidget {
  const _SearchLoading();

  @override
  Widget build(BuildContext context) {
    return const MnemoContentFrame(
      key: Key('search-loading'),
      maxWidth: 920,
      useSafeArea: false,
      child: MnemoSkeletonList(itemCount: 7),
    );
  }
}

class _SearchError extends StatelessWidget {
  const _SearchError({required this.message, required this.onRetry});

  final String message;
  final Future<void> Function() onRetry;

  @override
  Widget build(BuildContext context) {
    return MnemoContentFrame(
      key: const Key('search-error'),
      maxWidth: 920,
      useSafeArea: false,
      alignment: Alignment.center,
      child: MnemoErrorNotice(
        title: '搜索失败',
        message: message,
        onRetry: onRetry,
      ),
    );
  }
}

class _SearchFeedback extends StatelessWidget {
  const _SearchFeedback({
    super.key,
    required this.title,
    required this.message,
    required this.icon,
  });

  final String title;
  final String message;
  final IconData icon;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (BuildContext context, BoxConstraints constraints) {
        return ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          padding: EdgeInsets.zero,
          children: <Widget>[
            ConstrainedBox(
              constraints: BoxConstraints(minHeight: constraints.maxHeight),
              child: MnemoEmptyState(
                title: title,
                message: message,
                icon: icon,
              ),
            ),
          ],
        );
      },
    );
  }
}

class _SearchReady extends StatelessWidget {
  const _SearchReady({
    required this.listing,
    required this.refreshing,
    required this.refreshErrorMessage,
    required this.openingPath,
    required this.onRefresh,
    required this.onOpenResult,
  });

  final SearchListing listing;
  final bool refreshing;
  final String? refreshErrorMessage;
  final String? openingPath;
  final Future<void> Function() onRefresh;
  final Future<void> Function(SearchResultItem result) onOpenResult;

  @override
  Widget build(BuildContext context) {
    if (listing.results.isEmpty) {
      return _SearchEmpty(
        query: listing.query,
        refreshing: refreshing,
        refreshErrorMessage: refreshErrorMessage,
        onRefresh: onRefresh,
      );
    }

    return Align(
      alignment: Alignment.topCenter,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 920),
        child: RefreshIndicator(
          onRefresh: onRefresh,
          child: ListView(
            key: const Key('search-results'),
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.fromLTRB(
              MnemoSpacing.md,
              MnemoSpacing.md,
              MnemoSpacing.md,
              MnemoSpacing.xxl,
            ),
            children: <Widget>[
              _SearchSummary(count: listing.count, query: listing.query),
              if (refreshErrorMessage case final String message?) ...<Widget>[
                const SizedBox(height: MnemoSpacing.md),
                MnemoErrorNotice(
                  key: const Key('search-refresh-error'),
                  title: '结果可能已过期',
                  message: '刷新失败，当前显示上一次成功加载的结果。$message',
                  onRetry: onRefresh,
                ),
              ],
              if (listing.reachedLimit) ...<Widget>[
                const SizedBox(height: MnemoSpacing.md),
                const _SearchLimitNotice(),
              ],
              const SizedBox(height: MnemoSpacing.sm),
              for (final SearchResultItem result in listing.results)
                Padding(
                  padding: const EdgeInsets.only(bottom: 2),
                  child: _SearchResultTile(
                    result: result,
                    query: listing.query,
                    opening: openingPath == result.path,
                    enabled: openingPath == null,
                    onTap: () => onOpenResult(result),
                  ),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _SearchEmpty extends StatelessWidget {
  const _SearchEmpty({
    required this.query,
    required this.refreshing,
    required this.refreshErrorMessage,
    required this.onRefresh,
  });

  final String query;
  final bool refreshing;
  final String? refreshErrorMessage;
  final Future<void> Function() onRefresh;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (BuildContext context, BoxConstraints constraints) {
        final double feedbackHeight =
            constraints.maxHeight - (refreshErrorMessage == null ? 0 : 144);
        return RefreshIndicator(
          onRefresh: onRefresh,
          child: ListView(
            key: const Key('search-empty'),
            physics: const AlwaysScrollableScrollPhysics(),
            padding: const EdgeInsets.symmetric(horizontal: MnemoSpacing.md),
            children: <Widget>[
              if (refreshErrorMessage case final String message?) ...<Widget>[
                const SizedBox(height: MnemoSpacing.md),
                MnemoErrorNotice(
                  key: const Key('search-refresh-error'),
                  title: '结果可能已过期',
                  message: '刷新失败，当前显示上一次成功加载的结果。$message',
                  onRetry: onRefresh,
                ),
              ],
              ConstrainedBox(
                constraints: BoxConstraints(
                  minHeight: feedbackHeight > 0 ? feedbackHeight : 0,
                ),
                child: MnemoEmptyState(
                  title: '未找到匹配项',
                  message: '没有找到名称包含“$query”的文件或文件夹。',
                  icon: Icons.search_off_rounded,
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}

class _SearchSummary extends StatelessWidget {
  const _SearchSummary({required this.count, required this.query});

  final int count;
  final String query;

  @override
  Widget build(BuildContext context) {
    final TextTheme textTheme = Theme.of(context).textTheme;
    final Color color = Theme.of(context).colorScheme.onSurfaceVariant;
    return Semantics(
      header: true,
      child: Row(
        children: <Widget>[
          Expanded(
            child: Text(
              '“$query”的搜索结果',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: textTheme.titleMedium,
            ),
          ),
          const SizedBox(width: MnemoSpacing.sm),
          Text('$count 项', style: textTheme.bodyMedium?.copyWith(color: color)),
        ],
      ),
    );
  }
}

class _SearchLimitNotice extends StatelessWidget {
  const _SearchLimitNotice();

  @override
  Widget build(BuildContext context) {
    final ThemeData theme = Theme.of(context);
    final ColorScheme colors = theme.colorScheme;
    return Semantics(
      container: true,
      liveRegion: true,
      label: '已显示 100 项，结果可能未完全列出，请继续输入以缩小范围',
      child: Material(
        key: const Key('search-limit-notice'),
        color: colors.tertiaryContainer,
        borderRadius: BorderRadius.circular(MnemoRadius.md),
        child: Padding(
          padding: const EdgeInsets.all(MnemoSpacing.md),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              Icon(
                Icons.filter_alt_outlined,
                color: colors.onTertiaryContainer,
              ),
              const SizedBox(width: MnemoSpacing.sm),
              Expanded(
                child: Text(
                  '已显示 100 项，结果可能未完全列出，请继续输入以缩小范围。',
                  style: theme.textTheme.bodyMedium?.copyWith(
                    color: colors.onTertiaryContainer,
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

class _SearchResultTile extends StatelessWidget {
  const _SearchResultTile({
    required this.result,
    required this.query,
    required this.opening,
    required this.enabled,
    required this.onTap,
  });

  final SearchResultItem result;
  final String query;
  final bool opening;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final ColorScheme colors = Theme.of(context).colorScheme;
    return Semantics(
      button: true,
      enabled: enabled,
      label:
          '${result.isDirectory ? '文件夹' : '文件'} ${result.name}，位置 ${result.parentPath}',
      child: Material(
        color: Colors.transparent,
        borderRadius: BorderRadius.circular(MnemoRadius.sm),
        clipBehavior: Clip.antiAlias,
        child: ListTile(
          key: Key('search-result-${result.path}'),
          enabled: enabled,
          onTap: enabled ? onTap : null,
          minVerticalPadding: MnemoSpacing.sm,
          contentPadding: const EdgeInsets.symmetric(
            horizontal: MnemoSpacing.sm,
            vertical: MnemoSpacing.xxs,
          ),
          leading: _SearchResultIcon(result: result),
          title: _HighlightedName(name: result.name, query: query),
          subtitle: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              const SizedBox(height: MnemoSpacing.xxs),
              Text(
                result.parentPath,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 2),
              Text(
                result.isDirectory
                    ? '文件夹 · ${_formatModifiedAt(result.modifiedAt)}'
                    : '${_formatFileSize(result.size)} · ${_formatModifiedAt(result.modifiedAt)}',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: Theme.of(
                  context,
                ).textTheme.bodySmall?.copyWith(color: colors.onSurfaceVariant),
              ),
            ],
          ),
          trailing: opening
              ? const SizedBox.square(
                  key: Key('search-result-opening'),
                  dimension: 24,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Icon(
                  result.isDirectory
                      ? Icons.chevron_right_rounded
                      : Icons.open_in_new_rounded,
                  color: colors.onSurfaceVariant,
                ),
        ),
      ),
    );
  }
}

class _HighlightedName extends StatelessWidget {
  const _HighlightedName({required this.name, required this.query});

  final String name;
  final String query;

  @override
  Widget build(BuildContext context) {
    final TextStyle? style = Theme.of(context).textTheme.titleSmall;
    final RegExpMatch? match = query.isEmpty
        ? null
        : RegExp(
            RegExp.escape(query),
            caseSensitive: false,
            unicode: true,
          ).firstMatch(name);
    if (match == null) {
      return Text(
        name,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: style,
      );
    }

    final int start = match.start;
    final int end = match.end;
    return Text.rich(
      TextSpan(
        style: style,
        children: <InlineSpan>[
          if (start > 0) TextSpan(text: name.substring(0, start)),
          TextSpan(
            text: name.substring(start, end),
            style: style?.copyWith(
              color: Theme.of(context).colorScheme.primary,
              fontWeight: FontWeight.w700,
            ),
          ),
          if (end < name.length) TextSpan(text: name.substring(end)),
        ],
      ),
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
    );
  }
}

class _SearchResultIcon extends StatelessWidget {
  const _SearchResultIcon({required this.result});

  final SearchResultItem result;

  @override
  Widget build(BuildContext context) {
    final (IconData icon, Color color) = _searchResultVisual(result, context);
    return DecoratedBox(
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.13),
        borderRadius: BorderRadius.circular(MnemoRadius.sm),
      ),
      child: SizedBox.square(
        dimension: 44,
        child: Icon(icon, size: 24, color: color),
      ),
    );
  }
}

(IconData, Color) _searchResultVisual(
  SearchResultItem result,
  BuildContext context,
) {
  final ColorScheme colors = Theme.of(context).colorScheme;
  if (result.isDirectory) {
    return (Icons.folder_rounded, colors.primary);
  }
  final String extension = result.name.contains('.')
      ? result.name.split('.').last.toLowerCase()
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
  const List<String> units = <String>['KB', 'MB', 'GB', 'TB', 'PB'];
  double value = bytes / 1024;
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final int digits = value >= 100
      ? 0
      : value >= 10
      ? 1
      : 2;
  return '${value.toStringAsFixed(digits)} ${units[unit]}';
}

String _formatModifiedAt(DateTime value) {
  final DateTime local = value.toLocal();
  return '${local.year}年${local.month}月${local.day}日 '
      '${_twoDigits(local.hour)}:${_twoDigits(local.minute)}';
}

String _twoDigits(int value) => value.toString().padLeft(2, '0');
