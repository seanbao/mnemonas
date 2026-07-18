import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:mime/mime.dart';
import 'package:open_filex/open_filex.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:path_provider/path_provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../core/files/file_models.dart';
import '../core/files/file_path.dart';
import '../core/network/api_error.dart';
import '../core/search/search_models.dart';
import '../core/trash/trash_models.dart';
import '../design_system/design_system.dart';
import '../features/account/account_page.dart';
import '../features/auth/force_password_change_page.dart';
import '../features/auth/login_page.dart';
import '../features/connection/connection_page.dart';
import '../features/files/files_page.dart';
import '../features/home/home_page.dart';
import '../features/search/search_page.dart';
import '../features/shell/app_shell.dart';
import '../features/trash/trash_page.dart';
import '../platform/file_exporter.dart';
import '../platform/file_importer.dart';
import 'client_controller.dart';
import 'client_state.dart';

enum _UploadConflictChoice { replace, keepBoth, skip, cancel }

const int _largePreviewThresholdBytes = 128 * 1024 * 1024;

typedef _UploadPreparationProgress = void Function(int transferred, int? total);

final class _UploadCandidate {
  _UploadCandidate({
    required this.name,
    required Future<File> Function(_UploadPreparationProgress? onProgress)
    materialize,
    Future<void> Function()? cancelPreparation,
    this.deleteMaterializedFile = false,
  }) : _materializer = materialize,
       _preparationCancellation = cancelPreparation;

  final String name;
  final bool deleteMaterializedFile;
  final Future<File> Function(_UploadPreparationProgress? onProgress)
  _materializer;
  final Future<void> Function()? _preparationCancellation;
  File? _materialized;
  bool _released = false;

  bool get hasCancellablePreparation => _preparationCancellation != null;

  Future<File> materialize({_UploadPreparationProgress? onProgress}) async {
    if (_released) {
      throw StateError('The upload source has already been released');
    }
    final existing = _materialized;
    if (existing != null) {
      return existing;
    }
    final file = await _materializer(onProgress);
    _materialized = file;
    return file;
  }

  Future<void> cancelPreparation() async {
    await _preparationCancellation?.call();
  }

  Future<void> releaseBestEffort() async {
    if (_released) {
      return;
    }
    _released = true;
    final file = _materialized;
    if (deleteMaterializedFile && file != null) {
      try {
        if (await file.exists()) {
          await file.delete();
        }
      } on FileSystemException {
        // Cleanup failure must not replace the confirmed upload outcome.
      }
    }
  }
}

sealed class _UploadPreparationResult {
  const _UploadPreparationResult();
}

final class _UploadPreparationSuccess extends _UploadPreparationResult {
  const _UploadPreparationSuccess(this.file);

  final File file;
}

final class _UploadPreparationFailure extends _UploadPreparationResult {
  const _UploadPreparationFailure(this.error, this.stackTrace);

  final Object error;
  final StackTrace stackTrace;
}

class _UploadPreparationDialog extends StatefulWidget {
  const _UploadPreparationDialog({required this.candidate});

  final _UploadCandidate candidate;

  @override
  State<_UploadPreparationDialog> createState() =>
      _UploadPreparationDialogState();
}

class _UploadPreparationDialogState extends State<_UploadPreparationDialog> {
  int _transferred = 0;
  int? _total;
  bool _cancelling = false;
  bool _finished = false;
  String? _cancellationError;

  @override
  void initState() {
    super.initState();
    unawaited(_start());
  }

  Future<void> _start() async {
    try {
      final file = await widget.candidate.materialize(
        onProgress: (transferred, total) {
          if (!mounted || _finished) {
            return;
          }
          setState(() {
            _transferred = transferred;
            _total = total;
          });
        },
      );
      _close(_UploadPreparationSuccess(file));
    } on Object catch (error, stackTrace) {
      _close(_UploadPreparationFailure(error, stackTrace));
    }
  }

  Future<void> _cancel() async {
    if (_cancelling || _finished) {
      return;
    }
    setState(() {
      _cancelling = true;
      _cancellationError = null;
    });
    try {
      await widget.candidate.cancelPreparation();
    } on Object {
      if (mounted && !_finished) {
        setState(() {
          _cancelling = false;
          _cancellationError = '无法取消文件准备，请等待当前操作结束。';
        });
      }
    }
  }

  void _close(_UploadPreparationResult result) {
    if (!mounted || _finished) {
      return;
    }
    _finished = true;
    Navigator.of(context).pop(result);
  }

  Future<void> _cancelBestEffort() async {
    try {
      await widget.candidate.cancelPreparation();
    } on Object {
      // Disposal cannot surface a cancellation error after the route closed.
    }
  }

  @override
  void dispose() {
    if (!_finished) {
      unawaited(_cancelBestEffort());
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final total = _total;
    final progress = total != null && total > 0
        ? (_transferred / total).clamp(0.0, 1.0)
        : null;
    final detail = total != null && total >= 0
        ? '${_formatBytes(_transferred)} / ${_formatBytes(total)}'
        : '已处理 ${_formatBytes(_transferred)}';
    return PopScope(
      canPop: false,
      child: AlertDialog(
        title: const Text('正在准备上传文件'),
        content: SizedBox(
          width: 360,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: <Widget>[
              Text(
                widget.candidate.name,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: MnemoSpacing.md),
              LinearProgressIndicator(value: progress),
              const SizedBox(height: MnemoSpacing.sm),
              Text(detail),
              if (_cancellationError case final message?) ...<Widget>[
                const SizedBox(height: MnemoSpacing.sm),
                Text(
                  message,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              ],
            ],
          ),
        ),
        actions: <Widget>[
          TextButton(
            onPressed: _cancelling ? null : () => unawaited(_cancel()),
            child: Text(_cancelling ? '正在取消…' : '取消'),
          ),
        ],
      ),
    );
  }
}

class MnemoNasClientApp extends StatelessWidget {
  const MnemoNasClientApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'MnemoNAS',
      debugShowCheckedModeBanner: false,
      theme: MnemoTheme.light,
      darkTheme: MnemoTheme.dark,
      themeMode: ThemeMode.system,
      locale: const Locale('zh', 'CN'),
      supportedLocales: const <Locale>[Locale('zh', 'CN'), Locale('en')],
      localizationsDelegates: const <LocalizationsDelegate<dynamic>>[
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      home: const _ClientRoot(),
    );
  }
}

class _ClientRoot extends ConsumerWidget {
  const _ClientRoot();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(clientControllerProvider);
    final controller = ref.read(clientControllerProvider.notifier);
    ref.listen<String?>(
      clientControllerProvider.select((value) => value.notice),
      (previous, next) {
        if (next == null || next == previous) {
          return;
        }
        ScaffoldMessenger.of(context)
          ..hideCurrentSnackBar()
          ..showSnackBar(SnackBar(content: Text(next)));
      },
    );

    return switch (state.stage) {
      ClientStage.booting => const _BootPage(),
      ClientStage.needsConnection => ConnectionPage(
        initialAddress: state.endpoint?.baseUrl ?? '',
        onValidate: (endpoint) async {
          await controller.connect(endpoint.baseUrl);
          final connected = ref.read(clientControllerProvider);
          return ServerConnectionInfo(
            endpoint: endpoint,
            deviceName: connected.probe?.version.name,
            serverVersion: connected.probe?.version.version,
          );
        },
      ),
      ClientStage.unavailable => _UnavailablePage(
        message: state.errorMessage ?? '暂时无法连接设备。',
        onRetry: controller.retryConnection,
        onChangeServer: controller.changeServer,
      ),
      ClientStage.needsLogin => LoginPage(
        serverAddress: state.endpoint?.baseUrl ?? '',
        deviceName: state.probe?.version.name,
        onLogin: (credentials) => controller.login(
          username: credentials.username,
          password: credentials.password,
        ),
        onChangeServer: () => unawaited(controller.changeServer()),
      ),
      ClientStage.mandatoryPasswordChange => ForcePasswordChangePage(
        username: state.user?.username ?? '',
        onSubmit: (change) => controller.changePassword(
          currentPassword: change.currentPassword,
          newPassword: change.newPassword,
        ),
        onLogout: () => unawaited(controller.logout()),
      ),
      ClientStage.ready => _AuthenticatedHome(
        state: state,
        controller: controller,
      ),
    };
  }
}

class _BootPage extends StatelessWidget {
  const _BootPage();

  @override
  Widget build(BuildContext context) {
    return const Scaffold(
      body: MnemoContentFrame(
        alignment: Alignment.center,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            AppBrand(
              size: AppBrandSize.hero,
              layout: AppBrandLayout.stacked,
              subtitle: '正在连接私有存储',
            ),
            SizedBox(height: MnemoSpacing.xl),
            CircularProgressIndicator(),
          ],
        ),
      ),
    );
  }
}

class _UnavailablePage extends StatelessWidget {
  const _UnavailablePage({
    required this.message,
    required this.onRetry,
    required this.onChangeServer,
  });

  final String message;
  final Future<void> Function() onRetry;
  final Future<void> Function() onChangeServer;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: MnemoContentFrame(
        maxWidth: MnemoBreakpoint.readingMax,
        alignment: Alignment.center,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            MnemoErrorNotice(
              title: '无法连接到设备',
              message: message,
              onRetry: () => unawaited(onRetry()),
            ),
            const SizedBox(height: MnemoSpacing.sm),
            TextButton(
              onPressed: () => unawaited(onChangeServer()),
              child: const Text('更换设备'),
            ),
          ],
        ),
      ),
    );
  }
}

class _AuthenticatedHome extends StatefulWidget {
  const _AuthenticatedHome({required this.state, required this.controller});

  final ClientState state;
  final ClientController controller;

  @override
  State<_AuthenticatedHome> createState() => _AuthenticatedHomeState();
}

class _AuthenticatedHomeState extends State<_AuthenticatedHome> {
  AppDestination _destination = AppDestination.home;
  bool _searchOpen = false;
  int _searchPresentationGeneration = 0;
  String? _clientVersion;

  @override
  void initState() {
    super.initState();
    unawaited(_loadClientVersion());
  }

  Future<void> _loadClientVersion() async {
    try {
      final info = await PackageInfo.fromPlatform();
      if (mounted) {
        setState(() => _clientVersion = '${info.version}+${info.buildNumber}');
      }
    } on Object {
      // Version metadata is optional in development and widget-test hosts.
    }
  }

  @override
  Widget build(BuildContext context) {
    final destinationChild = switch (_destination) {
      AppDestination.home => HomePage(
        viewModel: HomeViewModel.ready(_homeSummary(widget.state)),
        onRefresh: widget.controller.refreshOverview,
        onOpenFiles: () => setState(() => _destination = AppDestination.files),
      ),
      AppDestination.files => FilesPage(
        viewModel: _filesViewModel(widget.state),
        onRefresh: () =>
            widget.controller.loadDirectory(widget.state.currentPath),
        onNavigatePath: (path) =>
            unawaited(widget.controller.loadDirectory(path)),
        onOpenFile: (entry) => unawaited(_openEntry(entry)),
        onAddAction: (action) => unawaited(_handleAddAction(action)),
        onFileAction: (request) => unawaited(_handleFileAction(request)),
      ),
      AppDestination.trash => TrashPage(
        viewModel: _trashViewModel(widget.state),
        onRefresh: _refreshTrash,
        onRestore: _restoreTrashItem,
        showSuccessMessages: false,
        onDeletePermanently: (selection) async {
          final outcome = await widget.controller.deleteTrashSelection(
            selection,
          );
          return TrashDeleteOutcome(
            deletedIds: outcome.deletedIds,
            skippedIds: outcome.skippedIds,
            remainingIds: outcome.remainingIds,
          );
        },
      ),
      AppDestination.account => AccountPage(
        viewModel: AccountViewModel(
          username: widget.state.user?.username ?? '当前账户',
          email: widget.state.user?.email,
          roleLabel: widget.state.user?.role == 'admin' ? '管理员' : '普通用户',
          deviceName: widget.state.probe?.version.name ?? 'MnemoNAS',
          serverAddress: widget.state.endpoint?.baseUrl ?? '',
          serverVersion: widget.state.probe?.version.version,
          clientVersion: _clientVersion,
        ),
        onChangeServer: () => unawaited(widget.controller.changeServer()),
        onLogout: widget.controller.logout,
        onChangePassword: () => unawaited(_changePassword()),
        onReportIssue: () => unawaited(_openIssueFeedback()),
      ),
    };
    final child = _searchOpen
        ? SearchPage(
            viewModel: _searchViewModel(widget.state),
            onSearch: _searchFiles,
            onClear: _clearSearch,
            onClose: _closeSearch,
            onOpenResult: _openSearchResult,
          )
        : destinationChild;
    return AppShell(
      destination: _destination,
      onDestinationSelected: _selectDestination,
      title: _searchOpen ? '搜索' : null,
      onSearch: _searchOpen ? null : _openSearch,
      actions: <Widget>[
        IconButton(
          onPressed: _showTransfers,
          tooltip: '传输记录',
          icon: Badge(
            isLabelVisible: widget.state.transfers.any(
              (transfer) =>
                  transfer.status != TransferStatus.completed &&
                  transfer.status != TransferStatus.cancelled,
            ),
            child: const Icon(Icons.swap_vert_rounded),
          ),
        ),
      ],
      child: child,
    );
  }

  FilesViewModel _filesViewModel(ClientState state) {
    final directory = state.directory;
    if (directory != null) {
      return FilesViewModel.fromListing(directory);
    }
    if (state.isBusy) {
      return FilesViewModel.loading(path: state.currentPath);
    }
    return FilesViewModel.error(
      path: state.currentPath,
      message: state.errorMessage ?? '目录加载失败',
    );
  }

  SearchViewModel _searchViewModel(ClientState state) {
    final listing = state.search;
    if (listing != null) {
      return SearchViewModel.ready(
        listing: listing,
        refreshing: state.isSearchBusy,
        refreshErrorMessage: state.searchErrorMessage,
      );
    }
    if (state.isSearchBusy) {
      return SearchViewModel.loading(state.searchQuery);
    }
    if (state.searchErrorMessage case final message?) {
      return SearchViewModel.error(query: state.searchQuery, message: message);
    }
    return SearchViewModel.idle(query: state.searchQuery);
  }

  TrashViewModel _trashViewModel(ClientState state) {
    final listing = state.trash;
    if (listing != null) {
      if (listing.items.isEmpty &&
          state.trashErrorMessage != null &&
          !state.trashReconciliationRequired) {
        return TrashViewModel.error(state.trashErrorMessage!);
      }
      return TrashViewModel.ready(
        listing: listing,
        canWrite:
            state.user?.role != 'guest' &&
            !state.isTrashBusy &&
            state.trashErrorMessage == null &&
            !state.trashReconciliationRequired,
        mutationBlockedMessage: state.trashReconciliationRequired
            ? state.trashErrorMessage ?? '刷新回收站成功前，已暂停恢复和永久删除操作。'
            : null,
        refreshErrorMessage:
            state.trashErrorMessage != null &&
                !state.trashReconciliationRequired
            ? state.trashErrorMessage
            : null,
      );
    }
    if (state.isTrashBusy) {
      return const TrashViewModel.loading();
    }
    if (state.trashErrorMessage case final message?) {
      return TrashViewModel.error(message);
    }
    return const TrashViewModel.loading();
  }

  void _selectDestination(AppDestination destination) {
    if (_searchOpen) {
      _searchPresentationGeneration++;
      widget.controller.cancelSearch();
    }
    setState(() {
      _destination = destination;
      _searchOpen = false;
    });
    if (destination == AppDestination.trash && widget.state.trash == null) {
      unawaited(_refreshTrash());
    }
  }

  Future<void> _searchFiles(String query) async {
    _searchPresentationGeneration++;
    try {
      await widget.controller.searchFiles(query);
    } on Object {
      // The controller exposes the search error through SearchViewModel.
    }
  }

  void _openSearch() {
    _searchPresentationGeneration++;
    setState(() => _searchOpen = true);
  }

  void _clearSearch() {
    _searchPresentationGeneration++;
    widget.controller.clearSearch();
  }

  void _closeSearch() {
    _searchPresentationGeneration++;
    widget.controller.cancelSearch();
    if (mounted) {
      setState(() => _searchOpen = false);
    }
  }

  Future<void> _openSearchResult(SearchResultItem result) async {
    final generation = ++_searchPresentationGeneration;
    try {
      final targetPath = result.isDirectory ? result.path : result.parentPath;
      final response = await widget.controller.resolveDirectoryForSearch(
        targetPath,
      );
      if (!_isSearchPresentationCurrent(generation)) {
        return;
      }
      final listing = response.data;
      if (result.isDirectory) {
        if (listing.path != result.path) {
          _showMessage('文件夹可能已移动或删除，请重新搜索。');
          return;
        }
        widget.controller.presentDirectoryListing(
          listing,
          persistenceWarning: response.warnings.isNotEmpty,
        );
        widget.controller.cancelSearch();
        setState(() {
          _destination = AppDestination.files;
          _searchOpen = false;
        });
        return;
      }

      FileEntry? currentEntry;
      if (listing.path == result.parentPath) {
        for (final entry in listing.entries) {
          if (entry.path == result.path && !entry.isDirectory) {
            currentEntry = entry;
            break;
          }
        }
      }
      if (currentEntry == null) {
        _showMessage('文件可能已移动或删除，请重新搜索。');
        return;
      }

      widget.controller.presentDirectoryListing(
        listing,
        persistenceWarning: response.warnings.isNotEmpty,
      );
      widget.controller.cancelSearch();
      setState(() {
        _destination = AppDestination.files;
        _searchOpen = false;
      });
      await _openEntry(currentEntry);
    } on Object catch (error) {
      if (_isSearchPresentationCurrent(generation)) {
        _showMessage(clientErrorMessage(error));
      }
    }
  }

  bool _isSearchPresentationCurrent(int generation) =>
      mounted && _searchOpen && generation == _searchPresentationGeneration;

  Future<void> _refreshTrash() async {
    try {
      await widget.controller.loadTrash();
    } on Object {
      // The controller exposes the loading error through TrashViewModel.
    }
  }

  Future<TrashRestoreOutcome> _restoreTrashItem(
    TrashItem item,
    String? destinationPath,
  ) async {
    try {
      await widget.controller.restoreTrashItem(
        item,
        destinationPath: destinationPath,
      );
      return TrashRestoreOutcome.restored;
    } on ApiException catch (error) {
      if (error.statusCode == 409) {
        return TrashRestoreOutcome.pathConflict;
      }
      rethrow;
    }
  }

  Future<void> _handleAddAction(FilesAddAction action) async {
    switch (action) {
      case FilesAddAction.createFolder:
        final name = await _promptForText(
          context,
          title: '新建文件夹',
          label: '文件夹名称',
          confirmLabel: '创建',
        );
        if (name == null) {
          return;
        }
        await _runAction(() => widget.controller.createDirectory(name));
      case FilesAddAction.uploadFiles:
        late final List<_UploadCandidate> selected;
        try {
          selected = await _selectUploadCandidates();
        } on Object catch (error) {
          _showMessage(clientErrorMessage(error));
          return;
        }
        if (selected.isEmpty) {
          return;
        }
        try {
          try {
            await widget.controller.loadDirectory(widget.state.currentPath);
          } on Object catch (error) {
            _showMessage(clientErrorMessage(error));
            return;
          }
          final listing = widget.controller.currentDirectory;
          if (listing == null) {
            _showMessage('无法确认当前目录状态，请刷新后重试。');
            return;
          }
          final reservedNames = listing.entries
              .map((entry) => entry.name)
              .toSet();
          final existingKinds = <String, bool>{
            for (final entry in listing.entries) entry.name: entry.isDirectory,
          };
          var uploaded = 0;
          var skipped = 0;
          try {
            for (final candidate in selected) {
              var targetName = candidate.name;
              final existingIsDirectory = existingKinds[targetName];
              if (existingIsDirectory != null) {
                final choice = await _resolveUploadConflict(
                  name: targetName,
                  isDirectory: existingIsDirectory,
                );
                if (!mounted || choice == _UploadConflictChoice.cancel) {
                  break;
                }
                if (choice == _UploadConflictChoice.skip) {
                  skipped++;
                  continue;
                }
                if (choice == _UploadConflictChoice.keepBoth) {
                  targetName = uniqueLogicalName(targetName, reservedNames);
                }
              }
              final localFile = await _materializeUploadCandidate(candidate);
              try {
                await widget.controller.uploadFile(
                  sourcePath: localFile.path,
                  fileName: targetName,
                );
              } finally {
                await candidate.releaseBestEffort();
              }
              uploaded++;
              reservedNames.add(targetName);
              existingKinds[targetName] = false;
            }
            _showMessage(
              skipped == 0
                  ? '已上传 $uploaded 个文件。'
                  : '已上传 $uploaded 个文件，已跳过 $skipped 个。',
            );
          } on Object catch (error) {
            _showMessage(
              uploaded == 0
                  ? clientErrorMessage(error)
                  : '已上传 $uploaded 个文件，后续上传失败。',
            );
          }
        } finally {
          for (final candidate in selected) {
            await candidate.releaseBestEffort();
          }
        }
    }
  }

  Future<List<_UploadCandidate>> _selectUploadCandidates() async {
    if (!Platform.isAndroid) {
      final files = await openFiles();
      return files
          .map(
            (file) => _UploadCandidate(
              name: file.name,
              materialize: (_) async => File(file.path),
            ),
          )
          .toList(growable: false);
    }

    final sources = await FileImporter.pickDocuments();
    if (sources.isEmpty) {
      return const <_UploadCandidate>[];
    }
    final root = await getApplicationSupportDirectory();
    final imports = Directory('${root.path}/mnemonas/imports');
    await imports.create(recursive: true);
    await _removeOldImportFiles(imports);
    final batch = DateTime.now().microsecondsSinceEpoch;
    return <_UploadCandidate>[
      for (var index = 0; index < sources.length; index++)
        _androidUploadCandidate(
          sources[index],
          '${imports.path}/$batch-$index-'
          '${_safeLocalFileName(sources[index].displayName)}',
        ),
    ];
  }

  _UploadCandidate _androidUploadCandidate(
    FileImportSource source,
    String localPath,
  ) {
    final operationId =
        'import-${DateTime.now().microsecondsSinceEpoch}-'
        '${source.uri.hashCode.toUnsigned(32)}';
    return _UploadCandidate(
      name: source.displayName,
      deleteMaterializedFile: true,
      materialize: (onProgress) => FileImporter.copyDocumentToFile(
        uri: source.uri,
        destinationPath: localPath,
        expectedLength: source.size,
        operationId: operationId,
        onProgress: onProgress,
      ),
      cancelPreparation: () => FileImporter.cancelCopy(operationId),
    );
  }

  Future<File> _materializeUploadCandidate(_UploadCandidate candidate) async {
    if (!candidate.hasCancellablePreparation) {
      return candidate.materialize();
    }
    final result = await showDialog<_UploadPreparationResult>(
      context: context,
      barrierDismissible: false,
      builder: (context) => _UploadPreparationDialog(candidate: candidate),
    );
    return switch (result) {
      _UploadPreparationSuccess(:final file) => file,
      _UploadPreparationFailure(:final error, :final stackTrace) =>
        Error.throwWithStackTrace(error, stackTrace),
      null => throw StateError('Upload preparation closed unexpectedly'),
    };
  }

  Future<void> _removeOldImportFiles(Directory directory) async {
    final cutoff = DateTime.now().subtract(const Duration(hours: 24));
    await for (final entity in directory.list(followLinks: false)) {
      if (entity is! File) {
        continue;
      }
      try {
        if ((await entity.lastModified()).isBefore(cutoff)) {
          await entity.delete();
        }
      } on FileSystemException {
        // An active or provider-owned import is left untouched.
      }
    }
  }

  Future<_UploadConflictChoice> _resolveUploadConflict({
    required String name,
    required bool isDirectory,
  }) async {
    return await showDialog<_UploadConflictChoice>(
          context: context,
          barrierDismissible: false,
          builder: (context) => AlertDialog(
            title: const Text('同名项目已存在'),
            content: Text(
              isDirectory
                  ? '“$name”是一个文件夹，不能直接替换。可为上传文件保留一个新名称，或跳过。'
                  : '“$name”已存在。替换会创建新版本（若服务器已启用版本记录）。',
            ),
            actions: <Widget>[
              TextButton(
                onPressed: () =>
                    Navigator.pop(context, _UploadConflictChoice.cancel),
                child: const Text('取消剩余上传'),
              ),
              TextButton(
                onPressed: () =>
                    Navigator.pop(context, _UploadConflictChoice.skip),
                child: const Text('跳过'),
              ),
              TextButton(
                onPressed: () =>
                    Navigator.pop(context, _UploadConflictChoice.keepBoth),
                child: const Text('保留两者'),
              ),
              if (!isDirectory)
                FilledButton(
                  onPressed: () =>
                      Navigator.pop(context, _UploadConflictChoice.replace),
                  child: const Text('替换'),
                ),
            ],
          ),
        ) ??
        _UploadConflictChoice.cancel;
  }

  Future<void> _handleFileAction(FileActionRequest request) async {
    switch (request.action) {
      case FileItemAction.download:
        if (request.entries.length == 1) {
          await _downloadEntry(request.entries.single);
        }
      case FileItemAction.rename:
        if (request.entries.length != 1) {
          return;
        }
        final entry = request.entries.single;
        final name = await _promptForText(
          context,
          title: '重命名',
          label: '新名称',
          initialValue: entry.name,
          confirmLabel: '保存',
        );
        if (name != null && name != entry.name) {
          await _runAction(() => widget.controller.renameEntry(entry, name));
        }
      case FileItemAction.move:
        await _moveOrCopy(request.entries, copy: false);
      case FileItemAction.copy:
        await _moveOrCopy(request.entries, copy: true);
      case FileItemAction.delete:
        await _deleteEntries(request.entries);
      case FileItemAction.details:
        if (request.entries.length == 1) {
          await _showFileDetails(request.entries.single);
        }
    }
  }

  Future<void> _moveOrCopy(
    List<FileEntry> entries, {
    required bool copy,
  }) async {
    final destination = await _promptForText(
      context,
      title: copy ? '复制到文件夹' : '移动到文件夹',
      label: '目标路径',
      initialValue: widget.state.currentPath,
      helperText: '输入以 / 开头的 MnemoNAS 文件夹路径。',
      confirmLabel: copy ? '复制' : '移动',
    );
    if (destination == null) {
      return;
    }
    await _runAction(
      () => copy
          ? widget.controller.copyEntries(entries, destination)
          : widget.controller.moveEntries(entries, destination),
    );
  }

  Future<void> _deleteEntries(List<FileEntry> entries) async {
    try {
      final snapshot = await widget.controller.prepareDelete(entries);
      if (!mounted) {
        return;
      }
      final trash = snapshot.policy.mode == DeleteMode.trash;
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          icon: Icon(
            trash ? Icons.delete_outline_rounded : Icons.delete_forever_rounded,
          ),
          title: Text(trash ? '移入回收站？' : '永久删除？'),
          content: Text(
            trash
                ? '${snapshot.targets.length} 项将移入回收站'
                      '${snapshot.policy.retentionDays > 0 ? '，保留 ${snapshot.policy.retentionDays} 天。' : '。'}'
                : '${snapshot.targets.length} 项将被永久删除，且无法从回收站恢复。',
          ),
          actions: <Widget>[
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              style: FilledButton.styleFrom(
                backgroundColor: Theme.of(context).colorScheme.error,
                foregroundColor: Theme.of(context).colorScheme.onError,
              ),
              child: Text(trash ? '移入回收站' : '永久删除'),
            ),
          ],
        ),
      );
      if (confirmed == true) {
        await _runAction(() => widget.controller.deleteConfirmed(snapshot));
      }
    } on Object catch (error) {
      _showMessage(clientErrorMessage(error));
    }
  }

  Future<void> _downloadEntry(FileEntry entry) async {
    if (entry.isDirectory) {
      _showMessage('当前版本暂不支持目录打包下载。');
      return;
    }
    final mimeType = lookupMimeType(entry.name) ?? 'application/octet-stream';
    try {
      if (Platform.isAndroid) {
        final transferId = await widget.controller.stageDownloadForDestination(
          entry: entry,
        );
        if (!mounted) {
          return;
        }
        final saved = await _selectDownloadDestination(
          transferId,
          entry.name,
          mimeType,
        );
        _showMessage(saved ? '文件已保存。' : '下载已完成，可在传输记录中选择保存位置。');
        return;
      }
      final target = await FileExporter.chooseTarget(
        suggestedName: entry.name,
        mimeType: mimeType,
      );
      if (target == null) {
        return;
      }
      await widget.controller.downloadFile(
        entry: entry,
        destinationPath: target.path!,
        overwrite: true,
      );
      _showMessage('文件已保存。');
    } on Object catch (error) {
      _showMessage(clientErrorMessage(error));
    }
  }

  Future<bool> _selectDownloadDestination(
    String transferId,
    String fileName,
    String mimeType,
  ) async {
    final target = await FileExporter.chooseTarget(
      suggestedName: fileName,
      mimeType: mimeType,
    );
    final uri = target?.contentUri;
    if (uri == null) {
      return false;
    }
    await widget.controller.setDownloadDestination(
      id: transferId,
      destinationUri: uri,
    );
    return true;
  }

  Future<void> _resumeTransfer(String id) async {
    ClientTransfer? transfer;
    for (final candidate in widget.state.transfers) {
      if (candidate.id == id) {
        transfer = candidate;
        break;
      }
    }
    if (Platform.isAndroid &&
        transfer?.status == TransferStatus.awaitingDestination) {
      final saved = await _selectDownloadDestination(
        id,
        transfer!.name,
        lookupMimeType(transfer.name) ?? 'application/octet-stream',
      );
      if (saved) {
        _showMessage('文件已保存。');
      }
      return;
    }
    await widget.controller.resumeTransfer(id);
  }

  Future<void> _openEntry(FileEntry entry) async {
    if (entry.isDirectory) {
      await widget.controller.loadDirectory(entry.path);
      return;
    }
    if (entry.size >= _largePreviewThresholdBytes) {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('下载后打开大文件？'),
          content: Text(
            '“${entry.name}”大小为 ${_formatBytes(entry.size)}。'
            '文件会先完整下载到应用缓存，下载期间请保持应用在前台。',
          ),
          actions: <Widget>[
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('继续下载'),
            ),
          ],
        ),
      );
      if (confirmed != true || !mounted) {
        return;
      }
    }
    try {
      final file = await _stagingFile('previews', entry.name);
      await _removeOldPreviewFiles(file.parent);
      final downloaded = await widget.controller.downloadFile(
        entry: entry,
        destinationPath: file.path,
        persistent: false,
      );
      if (Platform.isAndroid) {
        final result = await OpenFilex.open(
          downloaded.path,
          type: lookupMimeType(entry.name),
        );
        if (result.type != ResultType.done) {
          throw StateError(result.message);
        }
      } else {
        final opened = await launchUrl(
          Uri.file(downloaded.path),
          mode: LaunchMode.externalApplication,
        );
        if (!opened) {
          throw StateError('No application can open this file');
        }
      }
    } on Object catch (error) {
      _showMessage(clientErrorMessage(error));
    }
  }

  Future<File> _stagingFile(String folder, String name) async {
    final root = await getTemporaryDirectory();
    final directory = Directory('${root.path}/mnemonas/$folder');
    await directory.create(recursive: true);
    final safeName = _safeLocalFileName(name);
    return File(
      '${directory.path}/${DateTime.now().microsecondsSinceEpoch}-$safeName',
    );
  }

  Future<void> _removeOldPreviewFiles(Directory directory) async {
    final cutoff = DateTime.now().subtract(const Duration(hours: 24));
    await for (final entity in directory.list()) {
      if (entity is! File) {
        continue;
      }
      try {
        if ((await entity.lastModified()).isBefore(cutoff)) {
          await entity.delete();
        }
      } on FileSystemException {
        // Cache cleanup is best effort and must not block opening a file.
      }
    }
  }

  Future<void> _showFileDetails(FileEntry entry) {
    return showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(entry.name),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: <Widget>[
            Text(entry.isDirectory ? '类型：文件夹' : '类型：文件'),
            const SizedBox(height: MnemoSpacing.xs),
            Text('路径：${entry.path}'),
            if (!entry.isDirectory) ...<Widget>[
              const SizedBox(height: MnemoSpacing.xs),
              Text('大小：${_formatBytes(entry.size)}'),
            ],
            const SizedBox(height: MnemoSpacing.xs),
            Text('修改时间：${entry.modifiedAt.toLocal()}'),
          ],
        ),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
  }

  Future<void> _changePassword() async {
    final change = await showDialog<RequiredPasswordChange>(
      context: context,
      builder: (context) => const _PasswordChangeDialog(),
    );
    if (change == null) {
      return;
    }
    await _runAction(
      () => widget.controller.changePassword(
        currentPassword: change.currentPassword,
        newPassword: change.newPassword,
      ),
    );
  }

  Future<void> _openIssueFeedback() async {
    final opened = await launchUrl(
      Uri.parse('https://github.com/seanbao/mnemonas/issues/new/choose'),
      mode: LaunchMode.externalApplication,
    );
    if (!opened) {
      _showMessage('无法打开问题反馈页面。');
    }
  }

  Future<void> _runAction(Future<void> Function() action) async {
    try {
      await action();
    } on Object catch (error) {
      _showMessage(clientErrorMessage(error));
    }
  }

  void _showMessage(String message) {
    if (!mounted) {
      return;
    }
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  void _showTransfers() {
    unawaited(
      showModalBottomSheet<void>(
        context: context,
        showDragHandle: true,
        builder: (context) => Consumer(
          builder: (context, ref, _) {
            return _TransfersSheet(
              transfers: ref.watch(
                clientControllerProvider.select((state) => state.transfers),
              ),
              onCancel: ref
                  .read(clientControllerProvider.notifier)
                  .cancelTransfer,
              onResume: (id) => _runAction(() => _resumeTransfer(id)),
              onRemove: (id) => _runAction(
                () => ref
                    .read(clientControllerProvider.notifier)
                    .removeTransfer(id),
              ),
            );
          },
        ),
      ),
    );
  }
}

HomeSummary _homeSummary(ClientState state) {
  final stats = state.stats;
  final storage =
      stats?.diskStatsAvailable == true &&
          stats?.diskUsed != null &&
          stats?.diskTotal != null
      ? StorageSummary(
          usedBytes: stats!.diskUsed!,
          totalBytes: stats.diskTotal!,
        )
      : null;
  final alerts = <HomeAlert>[
    if (state.probe?.health.isHealthy == false)
      const HomeAlert(
        id: 'degraded',
        title: '设备状态需要检查',
        message: '一个或多个服务当前不可用，可在 Web 管理端查看详细状态。',
        severity: HomeAlertSeverity.warning,
      ),
    if (state.endpoint?.usesLanHttp == true)
      const HomeAlert(
        id: 'lan-http',
        title: '当前连接未加密',
        message: '局域网 HTTP 仅适用于可信网络，公网连接应配置 HTTPS。',
        severity: HomeAlertSeverity.warning,
      ),
    if (state.probe?.setup.isFirstRun == true)
      const HomeAlert(
        id: 'setup',
        title: '首次设置尚未完成',
        message: '可在 Web 管理端继续完成数据保护和访问设置。',
      ),
  ];
  return HomeSummary(
    deviceName: state.probe?.version.name ?? 'MnemoNAS',
    serverAddress: state.endpoint?.baseUrl ?? '',
    connectionStatus: NasConnectionStatus.online,
    storage: storage,
    alerts: alerts,
    updatedAt: state.probe?.health.timestamp,
  );
}

Future<String?> _promptForText(
  BuildContext context, {
  required String title,
  required String label,
  required String confirmLabel,
  String initialValue = '',
  String? helperText,
}) async {
  final controller = TextEditingController(text: initialValue);
  final formKey = GlobalKey<FormState>();
  try {
    return await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: Form(
          key: formKey,
          child: TextFormField(
            controller: controller,
            autofocus: true,
            textInputAction: TextInputAction.done,
            decoration: InputDecoration(
              labelText: label,
              helperText: helperText,
            ),
            validator: (value) =>
                value == null || value.trim().isEmpty ? '此项不能为空' : null,
            onFieldSubmitted: (_) {
              if (formKey.currentState?.validate() == true) {
                Navigator.pop(context, controller.text.trim());
              }
            },
          ),
        ),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              if (formKey.currentState?.validate() == true) {
                Navigator.pop(context, controller.text.trim());
              }
            },
            child: Text(confirmLabel),
          ),
        ],
      ),
    );
  } finally {
    controller.dispose();
  }
}

class _TransfersSheet extends StatelessWidget {
  const _TransfersSheet({
    required this.transfers,
    required this.onCancel,
    required this.onResume,
    required this.onRemove,
  });

  final List<ClientTransfer> transfers;
  final ValueChanged<String> onCancel;
  final Future<void> Function(String id) onResume;
  final Future<void> Function(String id) onRemove;

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: SizedBox(
        height: MediaQuery.sizeOf(context).height * 0.6,
        child: transfers.isEmpty
            ? const MnemoEmptyState(
                icon: Icons.swap_vert_rounded,
                title: '暂无传输记录',
                message: '上传或下载文件后，传输状态会显示在这里。',
              )
            : Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: <Widget>[
                  Padding(
                    padding: const EdgeInsets.symmetric(
                      horizontal: MnemoSpacing.lg,
                    ),
                    child: Text(
                      '传输记录',
                      style: Theme.of(context).textTheme.titleLarge,
                    ),
                  ),
                  const SizedBox(height: MnemoSpacing.sm),
                  Expanded(
                    child: ListView.separated(
                      itemCount: transfers.length,
                      separatorBuilder: (_, _) => const Divider(height: 1),
                      itemBuilder: (context, index) {
                        final transfer = transfers[index];
                        return ListTile(
                          leading: Icon(
                            transfer.direction == TransferDirection.upload
                                ? Icons.upload_rounded
                                : Icons.download_rounded,
                          ),
                          title: Text(
                            transfer.name,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                          subtitle: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: <Widget>[
                              Text(_transferStatusLabel(transfer)),
                              if (transfer.status == TransferStatus.running)
                                Padding(
                                  padding: const EdgeInsets.only(
                                    top: MnemoSpacing.xxs,
                                  ),
                                  child: LinearProgressIndicator(
                                    value: transfer.progress,
                                  ),
                                ),
                            ],
                          ),
                          trailing: _TransferAction(
                            transfer: transfer,
                            onCancel: onCancel,
                            onResume: onResume,
                            onRemove: onRemove,
                          ),
                        );
                      },
                    ),
                  ),
                ],
              ),
      ),
    );
  }
}

String _transferStatusLabel(ClientTransfer transfer) {
  return switch (transfer.status) {
    TransferStatus.queued => '等待传输',
    TransferStatus.running =>
      transfer.total > 0
          ? '${_formatBytes(transfer.transferred)} / ${_formatBytes(transfer.total)}'
          : '正在传输',
    TransferStatus.paused => transfer.errorMessage ?? '已暂停，可继续传输',
    TransferStatus.awaitingAuth => '等待重新登录后继续',
    TransferStatus.awaitingDestination =>
      transfer.errorMessage ?? '下载完成，等待写入所选位置',
    TransferStatus.resultUnconfirmed =>
      transfer.errorMessage ?? '操作结果待确认，请核对目标位置',
    TransferStatus.completed => '已完成',
    TransferStatus.failed => transfer.errorMessage ?? '传输失败',
    TransferStatus.cancelled => '已取消',
  };
}

class _TransferAction extends StatelessWidget {
  const _TransferAction({
    required this.transfer,
    required this.onCancel,
    required this.onResume,
    required this.onRemove,
  });

  final ClientTransfer transfer;
  final ValueChanged<String> onCancel;
  final Future<void> Function(String id) onResume;
  final Future<void> Function(String id) onRemove;

  @override
  Widget build(BuildContext context) {
    return switch (transfer.status) {
      TransferStatus.running => IconButton(
        onPressed: () => onCancel(transfer.id),
        tooltip: '暂停传输',
        icon: const Icon(Icons.pause_rounded),
      ),
      TransferStatus.paused ||
      TransferStatus.awaitingAuth ||
      TransferStatus.awaitingDestination => IconButton(
        onPressed: () => unawaited(onResume(transfer.id)),
        tooltip: '继续传输',
        icon: const Icon(Icons.play_arrow_rounded),
      ),
      TransferStatus.completed ||
      TransferStatus.failed ||
      TransferStatus.cancelled ||
      TransferStatus.resultUnconfirmed => IconButton(
        onPressed: () => unawaited(onRemove(transfer.id)),
        tooltip: '移除记录',
        icon: const Icon(Icons.close_rounded),
      ),
      TransferStatus.queued => const SizedBox(width: 48, height: 48),
    };
  }
}

class _PasswordChangeDialog extends StatefulWidget {
  const _PasswordChangeDialog();

  @override
  State<_PasswordChangeDialog> createState() => _PasswordChangeDialogState();
}

class _PasswordChangeDialogState extends State<_PasswordChangeDialog> {
  final _formKey = GlobalKey<FormState>();
  final _currentController = TextEditingController();
  final _newController = TextEditingController();
  final _confirmController = TextEditingController();
  bool _visible = false;

  @override
  void dispose() {
    _currentController.dispose();
    _newController.dispose();
    _confirmController.dispose();
    super.dispose();
  }

  String? _validateNewPassword(String? value) {
    final password = value ?? '';
    final length = utf8.encode(password).length;
    if (password.trim().isEmpty || length < 8) {
      return '新密码至少包含 8 个 UTF-8 字节';
    }
    if (length > 72) {
      return '新密码不能超过 72 个 UTF-8 字节';
    }
    if (password == _currentController.text) {
      return '新密码不能与当前密码相同';
    }
    return null;
  }

  void _submit() {
    if (_formKey.currentState?.validate() != true) {
      return;
    }
    Navigator.pop(
      context,
      RequiredPasswordChange(
        currentPassword: _currentController.text,
        newPassword: _newController.text,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('修改密码'),
      content: SingleChildScrollView(
        child: Form(
          key: _formKey,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: <Widget>[
              TextFormField(
                controller: _currentController,
                obscureText: !_visible,
                textInputAction: TextInputAction.next,
                decoration: const InputDecoration(labelText: '当前密码'),
                validator: (value) =>
                    value == null || value.isEmpty ? '请输入当前密码' : null,
              ),
              const SizedBox(height: MnemoSpacing.sm),
              TextFormField(
                controller: _newController,
                obscureText: !_visible,
                textInputAction: TextInputAction.next,
                decoration: const InputDecoration(labelText: '新密码'),
                validator: _validateNewPassword,
              ),
              const SizedBox(height: MnemoSpacing.sm),
              TextFormField(
                controller: _confirmController,
                obscureText: !_visible,
                textInputAction: TextInputAction.done,
                decoration: InputDecoration(
                  labelText: '确认新密码',
                  suffixIcon: IconButton(
                    onPressed: () => setState(() => _visible = !_visible),
                    tooltip: _visible ? '隐藏密码' : '显示密码',
                    icon: Icon(
                      _visible
                          ? Icons.visibility_off_outlined
                          : Icons.visibility_outlined,
                    ),
                  ),
                ),
                validator: (value) =>
                    value != _newController.text ? '两次输入的密码不一致' : null,
                onFieldSubmitted: (_) => _submit(),
              ),
            ],
          ),
        ),
      ),
      actions: <Widget>[
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(onPressed: _submit, child: const Text('修改')),
      ],
    );
  }
}

String _safeLocalFileName(String name) {
  final sanitized = name.replaceAll(RegExp(r'[\x00-\x1f\x7f/\\]'), '_').trim();
  if (sanitized.isEmpty || sanitized == '.' || sanitized == '..') {
    return 'MnemoNAS-download';
  }
  return sanitized.length > 180 ? sanitized.substring(0, 180) : sanitized;
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
