import 'dart:async';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path_provider/path_provider.dart';

import '../core/auth/auth_api.dart';
import '../core/auth/session_store.dart';
import '../core/files/file_models.dart';
import '../core/files/file_path.dart';
import '../core/files/files_api.dart';
import '../core/network/api_client.dart';
import '../core/network/api_error.dart';
import '../core/search/search_api.dart';
import '../core/search/search_models.dart';
import '../core/server/server_endpoint.dart';
import '../core/server/server_preferences.dart';
import '../core/system/system_api.dart';
import '../core/system/system_models.dart';
import '../core/transfers/transfers.dart' as transfer_core;
import '../core/trash/trash_api.dart';
import '../core/trash/trash_models.dart';
import '../platform/file_exporter.dart';
import 'client_state.dart';

final serverPreferencesProvider = Provider<ServerPreferencesStore>(
  (ref) => ServerPreferences(),
);

final authSessionStoreProvider = Provider<AuthSessionStore>(
  (ref) => SecureAuthSessionStore(),
);

typedef TransferStoreFactory =
    Future<transfer_core.FileTransferStore> Function();

typedef TransferStartBarrier = Future<void> Function();

typedef ContentUriMaterializer =
    Future<void> Function({
      required String sourcePath,
      required String destinationUri,
      required String operationId,
      required void Function(int transferred, int? total) onProgress,
    });

typedef ContentUriMaterializationCanceller =
    Future<void> Function(String operationId);

final class _TransferPauseReason {
  const _TransferPauseReason({
    required this.code,
    required this.message,
    required this.destinationMessage,
    required this.cancellationMessage,
  });

  final String code;
  final String message;
  final String destinationMessage;
  final String cancellationMessage;
}

const _appBackgroundedTransferPause = _TransferPauseReason(
  code: 'APP_BACKGROUNDED',
  message: '应用进入后台，传输已暂停，可返回前台后继续。',
  destinationMessage: '应用进入后台，保存已暂停，请重新选择保存位置。',
  cancellationMessage: 'Transfer paused because the app entered background',
);

const _appBackgroundedTransferException = ApiException(
  kind: ApiFailureKind.cancelled,
  code: 'APP_BACKGROUNDED',
  message: 'Transfer start was paused because the app entered background',
);

final transferStoreFactoryProvider = Provider<TransferStoreFactory>((ref) {
  return () async {
    final root = await getApplicationSupportDirectory();
    final separator = Platform.pathSeparator;
    return transfer_core.FileTransferStore(
      directoryPath: '${root.path}${separator}mnemonas${separator}transfers',
    );
  };
});

@visibleForTesting
final transferStartBarrierProvider = Provider<TransferStartBarrier?>(
  (ref) => null,
);

final contentUriMaterializerProvider = Provider<ContentUriMaterializer>((ref) {
  return ({
    required sourcePath,
    required destinationUri,
    required operationId,
    required onProgress,
  }) {
    return FileExporter.exportStagedFile(
      sourcePath: sourcePath,
      target: FileExportTarget.contentUri(destinationUri),
      operationId: operationId,
      onProgress: onProgress,
    );
  };
});

final contentUriMaterializationCancellerProvider =
    Provider<ContentUriMaterializationCanceller>((ref) {
      return FileExporter.cancelExport;
    });

typedef ApiClientFactory =
    ApiClient Function(ServerEndpoint endpoint, AuthSessionStore sessionStore);

const int maxDurableUploadBytes = 10 * 1024 * 1024 * 1024;
const String versionRestoreResultUnconfirmedCode =
    'VERSION_RESTORE_RESULT_UNCONFIRMED';
const String versionRestoreConfirmedRefreshRequiredCode =
    'VERSION_RESTORE_CONFIRMED_REFRESH_REQUIRED';

final class TrashSelectionOutcome {
  TrashSelectionOutcome({
    required List<String> deletedIds,
    required List<String> skippedIds,
    required List<String> remainingIds,
    required this.reconciled,
    required this.hasWarnings,
  }) : deletedIds = List<String>.unmodifiable(deletedIds),
       skippedIds = List<String>.unmodifiable(skippedIds),
       remainingIds = List<String>.unmodifiable(remainingIds);

  final List<String> deletedIds;
  final List<String> skippedIds;
  final List<String> remainingIds;
  final bool reconciled;
  final bool hasWarnings;

  bool get isPartial => remainingIds.isNotEmpty;
}

final apiClientFactoryProvider = Provider<ApiClientFactory>(
  (ref) =>
      (endpoint, sessionStore) =>
          ApiClient(endpoint: endpoint, sessionStore: sessionStore),
);

final clientControllerProvider =
    NotifierProvider<ClientController, ClientState>(ClientController.new);

class ClientController extends Notifier<ClientState> {
  late final ServerPreferencesStore _serverPreferences;
  late final AuthSessionStore _sessionStore;
  late final ApiClientFactory _apiClientFactory;
  late final TransferStoreFactory _transferStoreFactory;
  late final TransferStartBarrier? _transferStartBarrier;
  late final ContentUriMaterializer _contentUriMaterializer;
  late final ContentUriMaterializationCanceller
  _contentUriMaterializationCanceller;

  ApiClient? _client;
  AuthApi? _auth;
  FilesApi? _files;
  SearchApi? _search;
  SystemApi? _system;
  TrashApi? _trash;
  CancelToken? _directoryCancellation;
  CancelToken? _overviewCancellation;
  CancelToken? _searchCancellation;
  CancelToken? _searchTargetCancellation;
  CancelToken? _trashReadCancellation;
  CancelToken? _trashMutationCancellation;
  CancelToken? _versionRestoreCancellation;
  Object? _fileMutationLease;
  final Set<String> _fileMutationReconciliationPaths = <String>{};
  Future<void> _preferenceMutationTail = Future<void>.value();
  var _contextEpoch = 0;
  var _directorySequence = 0;
  var _searchSequence = 0;
  var _searchTargetSequence = 0;
  var _trashSequence = 0;
  var _transferSequence = 0;
  var _foregroundTransferGeneration = 0;
  var _disposed = false;
  final Map<String, CancelToken> _transferCancellations =
      <String, CancelToken>{};
  final Map<CancelToken, _TransferPauseReason> _transferPauseReasons =
      <CancelToken, _TransferPauseReason>{};
  final Set<CancelToken> _settledTransferCancellations = <CancelToken>{};
  final Set<CancelToken> _versionDownloadCancellations = <CancelToken>{};
  final Map<String, transfer_core.TransferTask> _durableTransfers =
      <String, transfer_core.TransferTask>{};
  final Set<String> _transferLeases = <String>{};
  Future<transfer_core.FileTransferStore>? _transferStoreFuture;

  DirectoryListing? get currentDirectory => state.directory;

  @override
  ClientState build() {
    _serverPreferences = ref.watch(serverPreferencesProvider);
    _sessionStore = ref.watch(authSessionStoreProvider);
    _apiClientFactory = ref.watch(apiClientFactoryProvider);
    _transferStoreFactory = ref.watch(transferStoreFactoryProvider);
    _transferStartBarrier = ref.watch(transferStartBarrierProvider);
    _contentUriMaterializer = ref.watch(contentUriMaterializerProvider);
    _contentUriMaterializationCanceller = ref.watch(
      contentUriMaterializationCancellerProvider,
    );
    ref.onDispose(_disposeController);
    unawaited(Future<void>.microtask(_bootstrap));
    return const ClientState.booting();
  }

  Future<void> _bootstrap() async {
    final epoch = _contextEpoch;
    try {
      final endpoint = await _serverPreferences.load();
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (endpoint == null) {
        state = const ClientState(stage: ClientStage.needsConnection);
        return;
      }
      await _restoreEndpoint(endpoint);
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
      }
      state = ClientState(
        stage: ClientStage.needsConnection,
        errorMessage: clientErrorMessage(error),
      );
    }
  }

  Future<void> _restoreEndpoint(ServerEndpoint endpoint) async {
    final epoch = _configure(endpoint);
    final system = _system!;
    final auth = _auth!;
    state = ClientState(
      stage: ClientStage.booting,
      endpoint: endpoint,
      isBusy: true,
    );
    try {
      final probe = await system.probe();
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (!probe.setup.authEnabled) {
        state = ClientState(
          stage: ClientStage.unavailable,
          endpoint: endpoint,
          probe: probe,
          errorMessage: '当前服务器未启用用户认证。请先在 Web 管理端启用认证。',
        );
        return;
      }

      final stored = (await _sessionStore.snapshot()).session;
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (stored == null || stored.serverBaseUrl != endpoint.baseUrl) {
        state = ClientState(
          stage: ClientStage.needsLogin,
          endpoint: endpoint,
          probe: probe,
        );
        return;
      }

      try {
        final me = await auth.me();
        if (!_isContextCurrent(epoch)) {
          return;
        }
        if (me.data.mustChangePassword) {
          state = ClientState(
            stage: ClientStage.mandatoryPasswordChange,
            endpoint: endpoint,
            probe: probe,
            user: me.data,
          );
          return;
        }
        state = ClientState(
          stage: ClientStage.ready,
          endpoint: endpoint,
          probe: probe,
          user: me.data,
        );
        await _restoreDurableTransfers(expectedEpoch: epoch);
        await refreshOverview(expectedEpoch: epoch);
      } on ApiException catch (error) {
        if (!_isContextCurrent(epoch)) {
          return;
        }
        if (await handleAuthenticationFailure(
          error,
          endpoint: endpoint,
          probe: probe,
          expectedEpoch: epoch,
        )) {
          return;
        }
        if (error.statusCode == 401) {
          await _resetExpiredSession(
            endpoint: endpoint,
            probe: probe,
            expectedEpoch: epoch,
          );
          return;
        }
        rethrow;
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
      }
      state = ClientState(
        stage: ClientStage.unavailable,
        endpoint: endpoint,
        errorMessage: clientErrorMessage(error),
      );
    }
  }

  Future<void> connect(String value) async {
    final endpoint = ServerEndpoint.parse(value);
    final epoch = _configure(endpoint);
    final system = _system!;
    state = ClientState(
      stage: ClientStage.needsConnection,
      endpoint: endpoint,
      isBusy: true,
    );
    try {
      final probe = await system.probe();
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (!probe.setup.authEnabled) {
        throw const ApiException(
          kind: ApiFailureKind.local,
          code: 'AUTH_REQUIRED',
          message: 'User authentication must be enabled on the server',
        );
      }

      final previous = await _serverPreferences.load();
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (previous != endpoint) {
        await _client!.takeAndClearSession();
        if (!_isContextCurrent(epoch)) {
          return;
        }
      }
      await _serializePreferenceMutation(() async {
        if (!_isContextCurrent(epoch)) {
          return;
        }
        await _serverPreferences.save(endpoint);
      });
      if (!_isContextCurrent(epoch)) {
        return;
      }
      state = ClientState(
        stage: ClientStage.needsLogin,
        endpoint: endpoint,
        probe: probe,
        notice: endpoint.usesLanHttp ? '当前连接使用局域网 HTTP，传输内容未加密。' : null,
      );
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
      }
      state = ClientState(
        stage: ClientStage.needsConnection,
        endpoint: endpoint,
        errorMessage: clientErrorMessage(error),
      );
      rethrow;
    }
  }

  Future<void> login({
    required String username,
    required String password,
  }) async {
    final endpoint = state.endpoint;
    if (endpoint == null) {
      throw StateError('The server is not configured');
    }
    final probe = state.probe;
    final epoch = _configure(endpoint);
    final client = _client!;
    final auth = _auth!;
    state = ClientState(
      stage: ClientStage.needsLogin,
      endpoint: endpoint,
      probe: probe,
      isBusy: true,
    );
    try {
      // A new login attempt owns a fresh session revision. Any delayed result
      // from a superseded attempt can no longer commit its token pair.
      await client.takeAndClearSession();
      if (!_isContextCurrent(epoch)) {
        return;
      }
      final result = await auth.login(username: username, password: password);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      final nextStage = result.data.user.mustChangePassword
          ? ClientStage.mandatoryPasswordChange
          : ClientStage.ready;
      state = state.copyWith(
        stage: nextStage,
        user: result.data.user,
        isBusy: false,
        notice: result.warnings.isEmpty ? null : '登录已完成，但服务器报告了持久化警告。',
      );
      if (nextStage == ClientStage.ready) {
        await _restoreDurableTransfers(expectedEpoch: epoch);
        await refreshOverview(expectedEpoch: epoch);
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    }
  }

  Future<void> changePassword({
    required String currentPassword,
    required String newPassword,
  }) async {
    final user = state.user;
    final endpoint = state.endpoint;
    if (user == null || endpoint == null) {
      throw StateError('A signed-in user is required');
    }
    final epoch = _contextEpoch;
    final auth = _auth;
    if (auth == null) {
      throw StateError('The server is not configured');
    }
    state = state.copyWith(errorMessage: null);
    try {
      await auth.changePassword(
        currentPassword: currentPassword,
        newPassword: newPassword,
        expectedUserId: user.id,
      );
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        rethrow;
      }
      if (error is ApiException && error.isUnconfirmedMutation) {
        await _terminatePasswordSession(
          expectedEpoch: epoch,
          endpoint: endpoint,
          errorMessage: '无法确认密码修改结果。请先尝试使用新密码登录。',
          storageFailureMessage:
              '无法确认密码修改结果，也无法确认本机登录信息已持久清除。'
              '客户端已停止使用当前会话，请检查设备安全存储。',
        );
        rethrow;
      }
      if (!_isContextCurrent(epoch)) {
        return;
      }
      state = state.copyWith(errorMessage: clientErrorMessage(error));
      rethrow;
    }

    await _terminatePasswordSession(
      expectedEpoch: epoch,
      endpoint: endpoint,
      notice: '密码已修改。所有设备上的会话均已退出，请使用新密码登录。',
      storageFailureMessage:
          '密码已修改并已停止使用当前会话，但无法确认本机登录信息已持久清除。'
          '请检查设备安全存储后重新登录。',
    );
  }

  Future<void> logout() async {
    final endpoint = state.endpoint;
    if (endpoint == null) {
      await changeServer();
      return;
    }
    final epoch = _configure(endpoint);
    final auth = _auth!;
    state = state.copyWith(isBusy: true, errorMessage: null);
    String? warning;
    try {
      await auth.logout(
        onLocalSessionCleared: () {
          if (!_isContextCurrent(epoch)) {
            return;
          }
          state = ClientState(
            stage: ClientStage.needsLogin,
            endpoint: endpoint,
            probe: state.probe,
          );
        },
      );
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
      }
      warning =
          error is ApiException && error.code == 'AUTH_SESSION_STORAGE_FAILED'
          ? '无法确认本机登录信息已持久清除。客户端已停止使用当前会话，请检查设备安全存储。'
          : '服务器会话未能确认注销，本机登录信息已清除。';
    }
    if (!_isContextCurrent(epoch)) {
      return;
    }
    state = ClientState(
      stage: ClientStage.needsLogin,
      endpoint: endpoint,
      probe: state.probe,
      notice: warning,
    );
  }

  Future<void> retryConnection() async {
    final endpoint = state.endpoint;
    if (endpoint == null) {
      state = const ClientState(stage: ClientStage.needsConnection);
      return;
    }
    await _restoreEndpoint(endpoint);
  }

  Future<void> changeServer() async {
    final epoch = _invalidateContext();
    state = const ClientState(stage: ClientStage.needsConnection);
    Object? failure;
    try {
      await _sessionStore.takeAndClear();
    } on Object catch (error) {
      failure = error;
    }
    if (!_isContextCurrent(epoch)) {
      return;
    }
    try {
      await _serializePreferenceMutation(() async {
        if (!_isContextCurrent(epoch)) {
          return;
        }
        await _serverPreferences.clear();
      });
    } on Object catch (error) {
      failure ??= error;
    }
    if (!_isContextCurrent(epoch) || failure == null) {
      return;
    }
    state = ClientState(
      stage: ClientStage.needsConnection,
      errorMessage: clientErrorMessage(failure),
    );
  }

  Future<void> refreshOverview({int? expectedEpoch}) async {
    final epoch = expectedEpoch ?? _contextEpoch;
    if (!_isContextCurrent(epoch) || state.stage != ClientStage.ready) {
      return;
    }
    await loadDirectory(state.currentPath);
    if (!_isContextCurrent(epoch) || state.stage != ClientStage.ready) {
      return;
    }
    _overviewCancellation?.cancel('Superseded overview request');
    final cancellation = CancelToken();
    _overviewCancellation = cancellation;
    final system = _system;
    if (system == null) {
      return;
    }
    try {
      final stats = await system.stats(cancelToken: cancellation);
      if (!_isContextCurrent(epoch) ||
          !identical(_overviewCancellation, cancellation)) {
        return;
      }
      state = state.copyWith(stats: stats.data);
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) ||
          !identical(_overviewCancellation, cancellation) ||
          _isCancellation(error)) {
        return;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        return;
      }
      // File access remains usable when optional statistics are unavailable.
      state = state.copyWith(stats: null);
    } finally {
      if (identical(_overviewCancellation, cancellation)) {
        _overviewCancellation = null;
      }
    }
  }

  Future<void> loadDirectory(String path) async {
    final normalized = normalizeLogicalPath(path);
    final epoch = _contextEpoch;
    final sequence = ++_directorySequence;
    final previous = state.directory;
    final preservePrevious = previous?.path == normalized;
    _directoryCancellation?.cancel('Superseded directory request');
    final cancellation = CancelToken();
    _directoryCancellation = cancellation;
    final files = _requireFiles();
    state = state.copyWith(
      currentPath: normalized,
      directory: preservePrevious ? previous : null,
      isBusy: true,
      isDirectoryBusy: true,
      errorMessage: null,
      directoryErrorMessage: preservePrevious
          ? state.directoryErrorMessage
          : null,
    );
    try {
      final response = await files.list(normalized, cancelToken: cancellation);
      if (!_isDirectoryRequestCurrent(epoch, sequence, cancellation)) {
        return;
      }
      _fileMutationReconciliationPaths.remove(response.data.path);
      state = state.copyWith(
        currentPath: response.data.path,
        directory: response.data,
        isBusy: false,
        isDirectoryBusy: false,
        directoryErrorMessage: null,
        fileReconciliationRequired: _fileMutationReconciliationPaths.isNotEmpty,
        fileReconciliationMessage: _fileReconciliationMessage(),
        directoryUpdatedAt: DateTime.now().toUtc(),
        notice: response.warnings.isEmpty
            ? state.notice
            : '目录已加载，但服务器报告了持久化警告。',
      );
    } on Object catch (error) {
      if (!_isDirectoryRequestCurrent(epoch, sequence, cancellation) ||
          _isCancellation(error)) {
        return;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        final message = clientErrorMessage(error);
        state = state.copyWith(
          isBusy: false,
          isDirectoryBusy: false,
          errorMessage: message,
          directoryErrorMessage: message,
        );
      }
      rethrow;
    } finally {
      if (identical(_directoryCancellation, cancellation)) {
        _directoryCancellation = null;
      }
    }
  }

  Future<void> loadTrash() async {
    final activeMutation = _trashMutationCancellation;
    if (activeMutation != null && !activeMutation.isCancelled) {
      return;
    }
    final epoch = _contextEpoch;
    final sequence = ++_trashSequence;
    _trashReadCancellation?.cancel('Superseded trash request');
    final cancellation = CancelToken();
    _trashReadCancellation = cancellation;
    final trash = _requireTrash();
    state = state.copyWith(isTrashBusy: true, trashErrorMessage: null);
    try {
      final response = await trash.list(cancelToken: cancellation);
      if (!_isTrashRequestCurrent(epoch, sequence, cancellation)) {
        return;
      }
      state = state.copyWith(
        trash: response.data,
        isTrashBusy: false,
        trashReconciliationRequired: false,
        trashErrorMessage: null,
        notice: response.warnings.isEmpty
            ? state.notice
            : '回收站已加载，但服务器报告了持久化警告。',
      );
    } on Object catch (error) {
      if (!_isTrashRequestCurrent(epoch, sequence, cancellation) ||
          _isCancellation(error)) {
        return;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        state = state.copyWith(
          isTrashBusy: false,
          trashErrorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    } finally {
      if (identical(_trashReadCancellation, cancellation)) {
        _trashReadCancellation = null;
      }
    }
  }

  Future<void> searchFiles(
    String input, {
    int limit = defaultSearchResultLimit,
  }) async {
    final query = normalizeSearchQuery(input);
    final canonicalLimit = validateSearchResultLimit(limit);
    final epoch = _contextEpoch;
    final sequence = ++_searchSequence;
    ++_searchTargetSequence;
    _searchTargetCancellation?.cancel('Search query changed');
    _searchTargetCancellation = null;
    _searchCancellation?.cancel('Superseded search request');
    final cancellation = CancelToken();
    _searchCancellation = cancellation;
    final search = _requireSearch();
    final previous = state.search;
    final preservePrevious =
        previous != null &&
        previous.query == query &&
        previous.limit == canonicalLimit;
    state = state.copyWith(
      searchQuery: query,
      search: preservePrevious ? previous : null,
      isSearchBusy: true,
      searchErrorMessage: null,
    );
    try {
      final response = await search.search(
        query,
        limit: canonicalLimit,
        cancelToken: cancellation,
      );
      if (!_isSearchRequestCurrent(epoch, sequence, cancellation)) {
        return;
      }
      state = state.copyWith(
        search: response.data,
        isSearchBusy: false,
        searchErrorMessage: null,
      );
    } on Object catch (error) {
      if (!_isSearchRequestCurrent(epoch, sequence, cancellation) ||
          _isCancellation(error)) {
        return;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        state = state.copyWith(
          isSearchBusy: false,
          searchErrorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    } finally {
      if (identical(_searchCancellation, cancellation)) {
        _searchCancellation = null;
      }
    }
  }

  Future<ApiResponse<DirectoryListing>> resolveDirectoryForSearch(
    String path,
  ) async {
    final normalized = normalizeLogicalPath(path);
    final epoch = _contextEpoch;
    final sequence = ++_searchTargetSequence;
    _searchTargetCancellation?.cancel('Superseded search target request');
    final cancellation = CancelToken();
    _searchTargetCancellation = cancellation;
    final files = _requireFiles();
    try {
      final response = await files.list(normalized, cancelToken: cancellation);
      if (!_isSearchTargetRequestCurrent(epoch, sequence, cancellation)) {
        throw StateError('The search target request was superseded');
      }
      return response;
    } on Object catch (error) {
      if (_isSearchTargetRequestCurrent(epoch, sequence, cancellation) &&
          !_isCancellation(error)) {
        await handleAuthenticationFailure(error, expectedEpoch: epoch);
      }
      rethrow;
    } finally {
      if (identical(_searchTargetCancellation, cancellation)) {
        _searchTargetCancellation = null;
      }
    }
  }

  void presentDirectoryListing(
    DirectoryListing listing, {
    bool persistenceWarning = false,
  }) {
    if (state.stage != ClientStage.ready) {
      throw StateError('The authenticated client is not ready');
    }
    final normalized = normalizeLogicalPath(listing.path);
    if (normalized != listing.path) {
      throw const FormatException('Directory path must be canonical');
    }
    _directorySequence++;
    _directoryCancellation?.cancel('Resolved search target selected');
    _directoryCancellation = null;
    state = state.copyWith(
      currentPath: listing.path,
      directory: listing,
      isBusy: false,
      isDirectoryBusy: false,
      errorMessage: null,
      directoryErrorMessage: null,
      directoryUpdatedAt: DateTime.now().toUtc(),
      notice: persistenceWarning ? '目录已加载，但服务器报告了持久化警告。' : state.notice,
    );
  }

  void clearSearch() {
    ++_searchSequence;
    ++_searchTargetSequence;
    _searchCancellation?.cancel('Search cleared');
    _searchCancellation = null;
    _searchTargetCancellation?.cancel('Search cleared');
    _searchTargetCancellation = null;
    state = state.copyWith(
      searchQuery: '',
      search: null,
      isSearchBusy: false,
      searchErrorMessage: null,
    );
  }

  void cancelSearch() {
    ++_searchSequence;
    ++_searchTargetSequence;
    _searchCancellation?.cancel('Search closed');
    _searchCancellation = null;
    _searchTargetCancellation?.cancel('Search closed');
    _searchTargetCancellation = null;
    if (state.isSearchBusy) {
      state = state.copyWith(isSearchBusy: false);
    }
  }

  Future<FileVersionHistory> listVersionHistory(FileEntry entry) async {
    final target = _requireVersionTarget(entry);
    final epoch = _contextEpoch;
    final files = _requireFiles();
    try {
      final response = await files.listVersions(target.path);
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      final openedCurrentHash = target.openedCurrentHash;
      if (openedCurrentHash != null) {
        _requireMatchingCurrentVersion(
          response.data,
          expectedHash: openedCurrentHash,
        );
      }
      return response.data;
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      await handleAuthenticationFailure(error, expectedEpoch: epoch);
      rethrow;
    }
  }

  Future<File> downloadFileVersion({
    required FileEntry entry,
    required FileVersion version,
    required String destinationPath,
    bool overwrite = false,
  }) async {
    final target = _requireVersionTarget(entry);
    _requireVersionForTarget(version, target);
    final epoch = _contextEpoch;
    final files = _requireFiles();
    final cancellation = CancelToken();
    _versionDownloadCancellations.add(cancellation);
    try {
      final result = await files.downloadVersion(
        logicalPath: target.path,
        version: version,
        destinationPath: destinationPath,
        overwrite: overwrite,
        cancelToken: cancellation,
      );
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      return result.file;
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      await handleAuthenticationFailure(error, expectedEpoch: epoch);
      rethrow;
    } finally {
      _versionDownloadCancellations.remove(cancellation);
    }
  }

  Future<FileVersionHistory> restoreFileVersion({
    required FileEntry entry,
    required FileVersion version,
    required String expectedCurrentHash,
  }) async {
    final target = _requireVersionTarget(entry);
    _requireVersionForTarget(version, target);
    if (!_lowercaseBlake3DigestPattern.hasMatch(expectedCurrentHash)) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'VERSION_IDENTITY_INVALID',
        message: 'The expected current file identity is invalid',
      );
    }
    _ensureVersionRestoreAllowed();
    if (version.isCurrent || version.hash == expectedCurrentHash) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'VERSION_ALREADY_CURRENT',
        message: 'The selected version is already current',
      );
    }

    final epoch = _contextEpoch;
    final files = _requireFiles();
    final lease = _beginFileMutation();
    final cancellation = CancelToken();
    _versionRestoreCancellation = cancellation;
    try {
      late final FileVersionHistory beforeRestore;
      try {
        final directoryResponse = await files.list(
          target.parentPath,
          cancelToken: cancellation,
        );
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
        final currentEntry = _findFileEntry(
          directoryResponse.data,
          target.path,
        );
        if (currentEntry == null ||
            currentEntry.isDirectory ||
            !currentEntry.capabilities.concreteRead ||
            (currentEntry.contentHash != null &&
                currentEntry.contentHash != expectedCurrentHash)) {
          throw _versionTargetChanged();
        }

        final historyResponse = await files.listVersions(
          target.path,
          cancelToken: cancellation,
        );
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
        beforeRestore = historyResponse.data;
        _requireMatchingCurrentVersion(
          beforeRestore,
          expectedHash: expectedCurrentHash,
        );
        final selectedVersionStillExists = beforeRestore.versions.any(
          (candidate) => !candidate.isCurrent && candidate.hash == version.hash,
        );
        if (!selectedVersionStillExists) {
          throw const ApiException(
            kind: ApiFailureKind.local,
            code: 'VERSION_NOT_AVAILABLE',
            message: 'The selected historical version is no longer available',
          );
        }
      } on Object catch (error) {
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
        if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
          state = state.copyWith(errorMessage: clientErrorMessage(error));
        }
        rethrow;
      }

      late final ApiResponse<VersionRestoreResult> restoreResponse;
      try {
        restoreResponse = await files.restoreVersion(
          logicalPath: target.path,
          hash: version.hash,
          cancelToken: cancellation,
        );
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
      } on Object catch (error) {
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
        if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
          rethrow;
        }
        if (_isUnconfirmedVersionRestoreError(error)) {
          final unconfirmed = _versionRestoreResultUnconfirmed(error);
          _markFileMutationReconciliationRequired(<String>[target.parentPath]);
          state = state.copyWith(
            errorMessage: clientErrorMessage(unconfirmed),
            notice: null,
          );
          throw unconfirmed;
        }
        state = state.copyWith(errorMessage: clientErrorMessage(error));
        rethrow;
      }

      var refreshedHistory = beforeRestore;
      Object? historyRefreshError;
      try {
        final response = await files.listVersions(
          target.path,
          cancelToken: cancellation,
        );
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
        refreshedHistory = response.data;
      } on Object catch (error) {
        if (!_isContextCurrent(epoch)) {
          throw _operationSuperseded();
        }
        if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
          throw _operationSuperseded();
        }
        historyRefreshError = error;
      }

      final directoryRefreshed = await _refreshAfterConfirmedFileMutation(
        expectedEpoch: epoch,
        path: target.parentPath,
        staleNotice: '版本已确认恢复，但目录刷新失败，内容可能已过期。',
      );
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      if (historyRefreshError != null) {
        final refreshRequired = _versionRestoreConfirmedRefreshRequired(
          historyRefreshError,
        );
        state = state.copyWith(
          errorMessage: null,
          notice: clientErrorMessage(refreshRequired),
        );
        throw refreshRequired;
      }
      if (directoryRefreshed) {
        if (refreshedHistory.current.hash != version.hash) {
          state = state.copyWith(notice: '版本已恢复，但文件随后再次发生变化。当前列表已刷新。');
        } else if (restoreResponse.hasWarnings ||
            restoreResponse.data.persistenceWarning) {
          state = state.copyWith(notice: '版本已恢复，但服务器报告了持久化警告。');
        } else {
          state = state.copyWith(notice: '版本已恢复。');
        }
      }
      return refreshedHistory;
    } finally {
      if (identical(_versionRestoreCancellation, cancellation)) {
        _versionRestoreCancellation = null;
      }
      _finishFileMutation(lease);
    }
  }

  Future<void> restoreTrashItem(
    TrashItem item, {
    String? destinationPath,
  }) async {
    _ensureTrashMutationAllowed();
    final epoch = _contextEpoch;
    final trash = _requireTrash();
    final refreshPath = state.currentPath;
    final cancellation = CancelToken();
    _beginTrashMutation(cancellation);
    state = state.copyWith(isTrashBusy: true, trashErrorMessage: null);
    try {
      final response = await trash.restore(
        id: item.id,
        destinationPath: destinationPath,
        cancelToken: cancellation,
      );
      if (!_isTrashMutationCurrent(epoch, cancellation)) {
        throw _operationSuperseded();
      }
      state = state.copyWith(
        trash: state.trash?.withoutIds(<String>[item.id]),
        isTrashBusy: false,
        trashErrorMessage: null,
        notice: response.hasWarnings || response.data.persistenceWarning
            ? '项目已恢复，但服务器报告了持久化警告。'
            : '项目已恢复。',
      );
      try {
        await loadDirectory(refreshPath);
      } on Object {
        // The restore result is already confirmed. A directory refresh can be
        // retried independently and must not turn success into a failed action.
      }
    } on Object catch (error) {
      if (!_isTrashMutationCurrent(epoch, cancellation) ||
          _isCancellation(error)) {
        rethrow;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        rethrow;
      }
      if (_requiresTrashReconciliation(error)) {
        await _reconcileTrashAfterUnconfirmed(epoch);
      }
      if (!_isContextCurrent(epoch)) {
        rethrow;
      }
      state = state.copyWith(
        isTrashBusy: false,
        trashErrorMessage: state.trashReconciliationRequired
            ? state.trashErrorMessage
            : clientErrorMessage(error),
      );
      rethrow;
    } finally {
      if (identical(_trashMutationCancellation, cancellation)) {
        _trashMutationCancellation = null;
      }
    }
  }

  Future<TrashSelectionOutcome> deleteTrashSelection(
    TrashSelectionSnapshot selection,
  ) async {
    _ensureTrashMutationAllowed();
    final epoch = _contextEpoch;
    final trash = _requireTrash();
    final cancellation = CancelToken();
    _beginTrashMutation(cancellation);
    state = state.copyWith(isTrashBusy: true, trashErrorMessage: null);
    try {
      final response = await trash.emptySelection(
        selection,
        cancelToken: cancellation,
      );
      if (!_isTrashMutationCurrent(epoch, cancellation)) {
        throw _operationSuperseded();
      }
      final result = response.data;
      final remaining = List<String>.unmodifiable(result.remaining);
      final resolved = <String>[...result.deleted, ...result.skipped];
      state = state.copyWith(
        trash: state.trash?.withoutIds(resolved),
        isTrashBusy: false,
        trashErrorMessage: null,
        notice: _trashDeletionNotice(
          result,
          hasWarnings: response.hasWarnings || result.cleanupWarning,
        ),
      );
      return TrashSelectionOutcome(
        deletedIds: result.deleted,
        skippedIds: result.skipped,
        remainingIds: remaining,
        reconciled: false,
        hasWarnings:
            response.hasWarnings || result.cleanupWarning || result.partial,
      );
    } on Object catch (error) {
      if (!_isTrashMutationCurrent(epoch, cancellation) ||
          _isCancellation(error)) {
        rethrow;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        rethrow;
      }
      if (_requiresTrashReconciliation(error)) {
        await _reconcileTrashAfterUnconfirmed(epoch);
      }
      if (!_isContextCurrent(epoch)) {
        rethrow;
      }
      state = state.copyWith(
        isTrashBusy: false,
        trashErrorMessage: state.trashReconciliationRequired
            ? state.trashErrorMessage
            : clientErrorMessage(error),
      );
      rethrow;
    } finally {
      if (identical(_trashMutationCancellation, cancellation)) {
        _trashMutationCancellation = null;
      }
    }
  }

  Object _beginFileMutation() {
    _ensureFileMutationAllowed();
    if (_fileMutationLease != null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'FILE_MUTATION_IN_PROGRESS',
        message: 'Another file operation is still in progress',
      );
    }
    final lease = Object();
    _fileMutationLease = lease;
    state = state.copyWith(
      isBusy: true,
      isFileMutationBusy: true,
      errorMessage: null,
    );
    return lease;
  }

  void _ensureFileMutationAllowed() {
    if (_fileMutationReconciliationPaths.isNotEmpty ||
        state.fileReconciliationRequired) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'FILE_RECONCILIATION_REQUIRED',
        message:
            'Review and refresh every affected directory before another file mutation',
      );
    }
    if (state.isDirectoryBusy ||
        state.directory == null ||
        state.directory?.path != state.currentPath ||
        state.directoryErrorMessage != null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'DIRECTORY_REFRESH_REQUIRED',
        message: 'Refresh the current directory before modifying files',
      );
    }
  }

  void _markFileMutationReconciliationRequired(Iterable<String> paths) {
    for (final path in paths) {
      _fileMutationReconciliationPaths.add(normalizeLogicalPath(path));
    }
    state = state.copyWith(
      fileReconciliationRequired: _fileMutationReconciliationPaths.isNotEmpty,
      fileReconciliationMessage: _fileReconciliationMessage(),
    );
  }

  String? _fileReconciliationMessage() {
    if (_fileMutationReconciliationPaths.isEmpty) {
      return null;
    }
    final paths = _fileMutationReconciliationPaths.toList(growable: false)
      ..sort();
    return '文件操作结果待核对。请依次打开并刷新以下目录后再修改：${paths.join('、')}。';
  }

  void _finishFileMutation(Object lease) {
    if (!identical(_fileMutationLease, lease)) {
      return;
    }
    _fileMutationLease = null;
    if (!_disposed) {
      state = state.copyWith(
        isBusy: state.isDirectoryBusy,
        isFileMutationBusy: false,
      );
    }
  }

  Future<bool> _refreshAfterConfirmedFileMutation({
    required int expectedEpoch,
    required String path,
    required String staleNotice,
  }) async {
    if (!_isContextCurrent(expectedEpoch) || state.stage != ClientStage.ready) {
      return false;
    }
    if (state.currentPath != path) {
      state = state.copyWith(notice: '文件操作已完成，原目录未自动刷新。');
      return false;
    }
    try {
      await loadDirectory(path);
      final refreshed =
          _isContextCurrent(expectedEpoch) &&
          state.stage == ClientStage.ready &&
          state.currentPath == path &&
          state.directory?.path == path &&
          state.directoryErrorMessage == null &&
          !state.isDirectoryBusy;
      if (!refreshed &&
          _isContextCurrent(expectedEpoch) &&
          state.stage == ClientStage.ready &&
          state.currentPath != path) {
        state = state.copyWith(notice: '文件操作已完成，原目录未自动刷新。');
      }
      return refreshed;
    } on Object {
      if (_isContextCurrent(expectedEpoch) &&
          state.stage == ClientStage.ready) {
        state = state.copyWith(notice: staleNotice);
      }
      return false;
    }
  }

  Future<void> createDirectory(String name) async {
    final trimmed = name.trim();
    if (trimmed.isEmpty || trimmed.contains('/')) {
      throw const FormatException('文件夹名称不能为空或包含斜杠');
    }
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    final target = _joinLogicalPath(refreshPath, trimmed);
    final lease = _beginFileMutation();
    try {
      final result = await files.createDirectory(target);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      final refreshed = await _refreshAfterConfirmedFileMutation(
        expectedEpoch: epoch,
        path: refreshPath,
        staleNotice: '文件夹已创建，但列表刷新失败，内容可能已过期。',
      );
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (refreshed && (result.hasWarnings || result.data.persistenceWarning)) {
        state = state.copyWith(notice: '文件夹已创建，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
        return;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        if (_requiresFileMutationReconciliation(error)) {
          _markFileMutationReconciliationRequired(<String>[refreshPath]);
        }
        state = state.copyWith(
          isBusy: false,
          errorMessage: _fileMutationFailureMessage(error, '创建文件夹'),
        );
      }
      rethrow;
    } finally {
      _finishFileMutation(lease);
    }
  }

  Future<void> renameEntry(FileEntry entry, String newName) async {
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    final lease = _beginFileMutation();
    try {
      final result = await files.rename(
        logicalPath: entry.path,
        newName: newName,
      );
      if (!_isContextCurrent(epoch)) {
        return;
      }
      final refreshed = await _refreshAfterConfirmedFileMutation(
        expectedEpoch: epoch,
        path: refreshPath,
        staleNotice: '项目已重命名，但列表刷新失败，内容可能已过期。',
      );
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (refreshed && (result.hasWarnings || result.data.persistenceWarning)) {
        state = state.copyWith(notice: '项目已重命名，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
        return;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        if (_requiresFileMutationReconciliation(error)) {
          _markFileMutationReconciliationRequired(<String>[refreshPath]);
        }
        state = state.copyWith(
          isBusy: false,
          errorMessage: _fileMutationFailureMessage(error, '重命名'),
        );
      }
      rethrow;
    } finally {
      _finishFileMutation(lease);
    }
  }

  Future<void> moveEntries(
    List<FileEntry> entries,
    String destinationDirectory,
  ) {
    return _relocateEntries(entries, destinationDirectory, copy: false);
  }

  Future<void> copyEntries(
    List<FileEntry> entries,
    String destinationDirectory,
  ) {
    return _relocateEntries(entries, destinationDirectory, copy: true);
  }

  Future<void> _relocateEntries(
    List<FileEntry> entries,
    String destinationDirectory, {
    required bool copy,
  }) async {
    if (entries.isEmpty) {
      throw const FormatException('至少选择一个项目');
    }
    final destination = normalizeLogicalPath(destinationDirectory);
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    final lease = _beginFileMutation();
    var completed = 0;
    var hasWarnings = false;
    try {
      for (final entry in entries) {
        final target = _joinLogicalPath(destination, entry.name);
        final response = copy
            ? await files.copy(sourcePath: entry.path, destinationPath: target)
            : await files.move(sourcePath: entry.path, destinationPath: target);
        if (!_isContextCurrent(epoch)) {
          return;
        }
        completed++;
        hasWarnings =
            hasWarnings ||
            response.hasWarnings ||
            response.data.persistenceWarning;
      }
      final refreshed = await _refreshAfterConfirmedFileMutation(
        expectedEpoch: epoch,
        path: refreshPath,
        staleNotice: copy ? '项目已复制，但列表刷新失败，内容可能已过期。' : '项目已移动，但列表刷新失败，内容可能已过期。',
      );
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (refreshed && hasWarnings) {
        state = state.copyWith(
          notice: copy ? '项目已复制，但服务器报告了持久化警告。' : '项目已移动，但服务器报告了持久化警告。',
        );
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
        return;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        rethrow;
      }
      final requiresReconciliation = _requiresFileMutationReconciliation(error);
      if (requiresReconciliation) {
        _markFileMutationReconciliationRequired(<String>[
          refreshPath,
          destination,
        ]);
      } else {
        await _refreshAfterConfirmedFileMutation(
          expectedEpoch: epoch,
          path: refreshPath,
          staleNotice: '文件操作部分完成，且列表刷新失败，内容可能已过期。',
        );
      }
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (state.stage == ClientStage.needsLogin) {
        rethrow;
      }
      state = state.copyWith(
        errorMessage: completed == 0
            ? _fileMutationFailureMessage(error, copy ? '复制' : '移动')
            : '已完成 $completed 项，后续操作失败。请刷新目录确认结果。',
      );
      rethrow;
    } finally {
      _finishFileMutation(lease);
    }
  }

  Future<DeleteIntentSnapshot> prepareDelete(List<FileEntry> entries) async {
    final epoch = _contextEpoch;
    final files = _requireFiles();
    final lease = _beginFileMutation();
    try {
      final response = await files.prepareDeleteIntent(entries);
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      state = state.copyWith(isBusy: false);
      return response.data;
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
        rethrow;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
        );
      }
      rethrow;
    } finally {
      _finishFileMutation(lease);
    }
  }

  Future<void> deleteConfirmed(DeleteIntentSnapshot snapshot) async {
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    final lease = _beginFileMutation();
    var completed = 0;
    var hasWarnings = false;
    try {
      for (final confirmation in snapshot.confirmations) {
        final response = await files.delete(confirmation);
        if (!_isContextCurrent(epoch)) {
          return;
        }
        completed++;
        hasWarnings =
            hasWarnings || response.hasWarnings || response.data.hasWarning;
      }
      final refreshed = await _refreshAfterConfirmedFileMutation(
        expectedEpoch: epoch,
        path: refreshPath,
        staleNotice: '删除已完成，但列表刷新失败，内容可能已过期。',
      );
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (refreshed && hasWarnings) {
        state = state.copyWith(notice: '删除已完成，但服务器报告了清理或持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
        return;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        rethrow;
      }
      final requiresReconciliation = _requiresFileMutationReconciliation(error);
      if (requiresReconciliation) {
        _markFileMutationReconciliationRequired(<String>[refreshPath]);
      } else {
        await _refreshAfterConfirmedFileMutation(
          expectedEpoch: epoch,
          path: refreshPath,
          staleNotice: '删除操作部分完成，且列表刷新失败，内容可能已过期。',
        );
      }
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (state.stage == ClientStage.needsLogin) {
        rethrow;
      }
      state = state.copyWith(
        errorMessage: completed == 0
            ? _fileMutationFailureMessage(error, '删除')
            : '已删除 $completed 项，后续操作失败。请刷新目录确认结果。',
      );
      rethrow;
    } finally {
      _finishFileMutation(lease);
    }
  }

  Future<void> uploadFile({
    required String sourcePath,
    required String fileName,
    required String targetDirectory,
  }) async {
    final epoch = _contextEpoch;
    final foregroundGeneration = _foregroundTransferGeneration;
    final refreshPath = normalizeLogicalPath(targetDirectory);
    var task = await _createDurableUploadTask(
      sourcePath: sourcePath,
      fileName: fileName,
      remotePath: _joinLogicalPath(refreshPath, fileName),
      expectedForegroundGeneration: foregroundGeneration,
    );
    task = await _pauseDurableTransferBeforeStartIfBackgrounded(
      task,
      expectedForegroundGeneration: foregroundGeneration,
    );
    if (_isAppBackgroundedTask(task)) {
      throw _appBackgroundedTransferException;
    }
    final result = await _runDurableUpload(task);
    if (!_isContextCurrent(epoch)) {
      return;
    }
    if (state.currentPath != refreshPath) {
      state = state.copyWith(
        notice: result.persistenceWarning
            ? '文件已确认上传到 $refreshPath，但服务器报告了持久化警告。原目录未自动刷新。'
            : '文件已确认上传到 $refreshPath。原目录未自动刷新。',
      );
      return;
    }
    try {
      await loadDirectory(refreshPath);
    } on Object {
      state = state.copyWith(
        isBusy: false,
        errorMessage: null,
        notice: result.persistenceWarning
            ? '文件已确认上传，但服务器报告了持久化警告，且目录刷新失败。请手动刷新确认最新目录。'
            : '文件已确认上传，但目录刷新失败。请手动刷新确认最新目录。',
      );
      return;
    }
    if (result.persistenceWarning) {
      state = state.copyWith(notice: '文件已上传，但服务器报告了持久化警告。');
    }
  }

  Future<transfer_core.TransferTask> _createDurableUploadTask({
    required String sourcePath,
    required String fileName,
    required String remotePath,
    required int expectedForegroundGeneration,
  }) async {
    final epoch = _contextEpoch;
    final endpoint = state.endpoint;
    final user = state.user;
    if (endpoint == null || user == null || state.stage != ClientStage.ready) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    final source = File(sourcePath);
    final sourceInfo = await source.stat();
    if (sourceInfo.type != FileSystemEntityType.file || sourceInfo.size < 0) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_SOURCE_INVALID',
        message: 'The upload source is not a supported regular file',
      );
    }
    if (sourceInfo.size > maxDurableUploadBytes) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_SOURCE_TOO_LARGE',
        message: 'The upload source exceeds the 10 GiB limit',
      );
    }

    final id = _nextTransferId();
    final cancellation = CancelToken();
    _transferCancellations[id] = cancellation;
    _addTransfer(
      ClientTransfer(
        id: id,
        name: fileName,
        direction: TransferDirection.upload,
        status: TransferStatus.preparing,
        transferred: 0,
        total: sourceInfo.size,
      ),
      expectedEpoch: epoch,
    );

    String? stagingPath;
    try {
      final startBarrier = _transferStartBarrier;
      if (startBarrier != null) {
        await startBarrier();
      }
      _ensureForegroundTransferStart(
        expectedEpoch: epoch,
        expectedForegroundGeneration: expectedForegroundGeneration,
        cancellation: cancellation,
      );
      final store = await _ensureTransferStore();
      _ensureForegroundTransferStart(
        expectedEpoch: epoch,
        expectedForegroundGeneration: expectedForegroundGeneration,
        cancellation: cancellation,
      );
      await _pruneCompletedTransferHistory(store);
      _ensureForegroundTransferStart(
        expectedEpoch: epoch,
        expectedForegroundGeneration: expectedForegroundGeneration,
        cancellation: cancellation,
      );
      final payloadDirectory = Directory(
        '${store.directoryPath}${Platform.pathSeparator}payloads',
      );
      await payloadDirectory.create(recursive: true);
      _ensureForegroundTransferStart(
        expectedEpoch: epoch,
        expectedForegroundGeneration: expectedForegroundGeneration,
        cancellation: cancellation,
      );
      stagingPath =
          '${payloadDirectory.path}${Platform.pathSeparator}$id.upload';
      final staged = await _stageUploadPayload(
        source: source,
        destination: File(stagingPath),
        expectedBytes: sourceInfo.size,
        cancellation: cancellation,
        onProgress: (transferred) {
          _updateTransfer(
            id,
            transferred: transferred,
            total: sourceInfo.size,
            expectedEpoch: epoch,
          );
        },
      );
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      final pauseReason = _transferPauseReasons[cancellation];
      final backgrounded =
          expectedForegroundGeneration != _foregroundTransferGeneration ||
          pauseReason?.code == _appBackgroundedTransferPause.code;
      if (cancellation.isCancelled && !backgrounded) {
        throw const ApiException(
          kind: ApiFailureKind.cancelled,
          code: 'UPLOAD_PREPARATION_CANCELLED',
          message: 'Upload preparation was cancelled',
        );
      }
      final now = DateTime.now().toUtc();
      final task = transfer_core.TransferTask(
        id: id,
        direction: transfer_core.TransferDirection.upload,
        phase: backgrounded
            ? transfer_core.TransferPhase.paused
            : transfer_core.TransferPhase.queued,
        endpointBaseUrl: endpoint.baseUrl,
        userId: user.id,
        remotePath: remotePath,
        displayName: fileName,
        stagingPath: stagingPath,
        payloadSha256: staged.sha256,
        durableOffset: 0,
        totalBytes: staged.bytes,
        createdAt: now,
        updatedAt: now,
        errorCode: backgrounded ? _appBackgroundedTransferPause.code : null,
        errorMessage: backgrounded
            ? _appBackgroundedTransferPause.message
            : null,
      );
      await _persistDurableTask(task);
      return task;
    } on Object catch (error) {
      final path = stagingPath;
      if (path != null) {
        for (final candidate in <String>[path, '$path.part']) {
          try {
            final file = File(candidate);
            if (await file.exists()) {
              await file.delete();
            }
          } on FileSystemException {
            // A later orphan-payload cleanup can retry this private file.
          }
        }
      }
      if (_isContextCurrent(epoch)) {
        final backgrounded =
            error is ApiException &&
            error.code == _appBackgroundedTransferPause.code;
        final cancelled =
            !backgrounded &&
            error is ApiException &&
            error.kind == ApiFailureKind.cancelled;
        _updateTransfer(
          id,
          status: cancelled ? TransferStatus.cancelled : TransferStatus.failed,
          errorMessage: backgrounded
              ? _appBackgroundedTransferPause.message
              : cancelled
              ? null
              : clientErrorMessage(error),
          expectedEpoch: epoch,
        );
      }
      rethrow;
    } finally {
      if (identical(_transferCancellations[id], cancellation)) {
        _transferCancellations.remove(id);
      }
      _settledTransferCancellations.remove(cancellation);
      _transferPauseReasons.remove(cancellation);
    }
  }

  Future<_StagedUploadPayload> _stageUploadPayload({
    required File source,
    required File destination,
    required int expectedBytes,
    required CancelToken cancellation,
    required ValueChanged<int> onProgress,
  }) async {
    final partial = File('${destination.path}.part');
    if (await destination.exists() || await partial.exists()) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_STAGING_CONFLICT',
        message: 'The private upload staging path already exists',
      );
    }

    RandomAccessFile? output;
    final digestSink = _DigestCaptureSink();
    final hashSink = sha256.startChunkedConversion(digestSink);
    var written = 0;
    var completed = false;
    try {
      output = await partial.open(mode: FileMode.writeOnly);
      await for (final chunk in source.openRead()) {
        if (cancellation.isCancelled) {
          throw const ApiException(
            kind: ApiFailureKind.cancelled,
            code: 'UPLOAD_PREPARATION_CANCELLED',
            message: 'Upload preparation was cancelled',
          );
        }
        written += chunk.length;
        if (written > expectedBytes) {
          throw const ApiException(
            kind: ApiFailureKind.local,
            code: 'UPLOAD_SOURCE_CHANGED',
            message: 'The upload source changed while it was copied',
          );
        }
        await output.writeFrom(chunk);
        hashSink.add(chunk);
        onProgress(written);
      }
      if (written != expectedBytes) {
        throw const ApiException(
          kind: ApiFailureKind.local,
          code: 'UPLOAD_SOURCE_CHANGED',
          message: 'The upload source changed while it was copied',
        );
      }
      hashSink.close();
      await output.flush();
      await output.close();
      output = null;
      await partial.rename(destination.path);
      completed = true;
      return _StagedUploadPayload(
        bytes: written,
        sha256: digestSink.value.toString(),
      );
    } finally {
      await output?.close();
      if (!completed) {
        try {
          if (await partial.exists()) {
            await partial.delete();
          }
        } on FileSystemException {
          // The caller records the failure and retries private cleanup.
        }
      }
    }
  }

  Future<File> downloadFile({
    required FileEntry entry,
    String? destinationPath,
    String? destinationUri,
    bool overwrite = false,
    bool persistent = true,
  }) async {
    if ((destinationPath == null) == (destinationUri == null)) {
      throw ArgumentError(
        'Exactly one download destination path or content URI is required',
      );
    }
    if (!persistent) {
      if (destinationPath == null || destinationUri != null) {
        throw ArgumentError(
          'Ephemeral downloads require a local destination path',
        );
      }
      return _downloadEphemeral(
        entry: entry,
        destinationPath: destinationPath,
        overwrite: overwrite,
      );
    }

    final foregroundGeneration = _foregroundTransferGeneration;
    var task = await _createDurableDownloadTask(
      entry: entry,
      destinationPath: destinationPath,
      destinationUri: destinationUri,
      expectedForegroundGeneration: foregroundGeneration,
    );
    task = await _pauseDurableTransferBeforeStartIfBackgrounded(
      task,
      expectedForegroundGeneration: foregroundGeneration,
    );
    if (_isAppBackgroundedTask(task)) {
      throw _appBackgroundedTransferException;
    }
    return _runDurableDownload(task, overwrite: overwrite);
  }

  Future<String> stageDownloadForDestination({required FileEntry entry}) async {
    final foregroundGeneration = _foregroundTransferGeneration;
    var task = await _createDurableDownloadTask(
      entry: entry,
      expectedForegroundGeneration: foregroundGeneration,
    );
    task = await _pauseDurableTransferBeforeStartIfBackgrounded(
      task,
      expectedForegroundGeneration: foregroundGeneration,
    );
    if (_isAppBackgroundedTask(task)) {
      throw _appBackgroundedTransferException;
    }
    await _runDurableDownload(task);
    return task.id;
  }

  Future<File> setDownloadDestination({
    required String id,
    required String destinationUri,
  }) async {
    final foregroundGeneration = _foregroundTransferGeneration;
    final current = _durableTransfers[id];
    if (current == null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_NOT_FOUND',
        message: 'The transfer record is unavailable',
      );
    }
    if (_transferLeases.contains(id)) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_RUNNING',
        message: 'The transfer is already running',
      );
    }
    if (current.phase != transfer_core.TransferPhase.awaitingDestination ||
        current.direction != transfer_core.TransferDirection.download ||
        current.destinationPath != null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_DESTINATION_NOT_EXPECTED',
        message: 'The transfer is not waiting for a document destination',
      );
    }
    var task = current.copyWith(
      destinationUri: destinationUri,
      updatedAt: _nextTransferTimestamp(current),
      errorCode: null,
      errorMessage: null,
    );
    await _persistDurableTask(task);
    task = await _pauseDurableTransferBeforeStartIfBackgrounded(
      task,
      expectedForegroundGeneration: foregroundGeneration,
    );
    if (_isAppBackgroundedTask(task)) {
      throw _appBackgroundedTransferException;
    }
    return _runDurableDownload(task);
  }

  Future<transfer_core.TransferTask> _createDurableDownloadTask({
    required FileEntry entry,
    String? destinationPath,
    String? destinationUri,
    required int expectedForegroundGeneration,
  }) async {
    final endpoint = state.endpoint;
    final user = state.user;
    if (endpoint == null || user == null || state.stage != ClientStage.ready) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    final startBarrier = _transferStartBarrier;
    if (startBarrier != null) {
      await startBarrier();
    }
    final store = await _ensureTransferStore();
    await _pruneCompletedTransferHistory(store);
    final id = _nextTransferId();
    final payloadDirectory = Directory(
      '${store.directoryPath}${Platform.pathSeparator}payloads',
    );
    await payloadDirectory.create(recursive: true);
    final now = DateTime.now().toUtc();
    final backgrounded =
        expectedForegroundGeneration != _foregroundTransferGeneration;
    final task = transfer_core.TransferTask(
      id: id,
      direction: transfer_core.TransferDirection.download,
      phase: backgrounded
          ? transfer_core.TransferPhase.paused
          : transfer_core.TransferPhase.queued,
      endpointBaseUrl: endpoint.baseUrl,
      userId: user.id,
      remotePath: entry.path,
      displayName: entry.name,
      stagingPath:
          '${payloadDirectory.path}${Platform.pathSeparator}$id.payload',
      destinationPath: destinationPath,
      destinationUri: destinationUri,
      durableOffset: 0,
      totalBytes: entry.size,
      createdAt: now,
      updatedAt: now,
      errorCode: backgrounded ? _appBackgroundedTransferPause.code : null,
      errorMessage: backgrounded ? _appBackgroundedTransferPause.message : null,
    );
    await _persistDurableTask(task);
    return task;
  }

  Future<UploadSessionSnapshot> _runDurableUpload(
    transfer_core.TransferTask initialTask,
  ) async {
    if (!_transferLeases.add(initialTask.id)) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_RUNNING',
        message: 'The transfer is already running',
      );
    }
    final cancellation = CancelToken();
    _transferCancellations[initialTask.id] = cancellation;
    try {
      return await _runDurableUploadClaimed(
        initialTask,
        cancellation: cancellation,
      );
    } finally {
      if (identical(_transferCancellations[initialTask.id], cancellation)) {
        _transferCancellations.remove(initialTask.id);
      }
      _settledTransferCancellations.remove(cancellation);
      _transferPauseReasons.remove(cancellation);
      _transferLeases.remove(initialTask.id);
    }
  }

  Future<UploadSessionSnapshot> _runDurableUploadClaimed(
    transfer_core.TransferTask initialTask, {
    required CancelToken cancellation,
  }) async {
    var task = initialTask;
    final epoch = _contextEpoch;
    final endpoint = state.endpoint;
    final user = state.user;
    if (endpoint?.baseUrl != task.endpointBaseUrl ||
        user?.id != task.userId ||
        state.stage != ClientStage.ready) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_SCOPE_MISMATCH',
        message: 'The transfer belongs to another server or account',
      );
    }
    if (task.direction != transfer_core.TransferDirection.upload) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_DIRECTION_INVALID',
        message: 'The transfer is not an upload',
      );
    }
    if (task.isTerminal && task.phase != transfer_core.TransferPhase.failed) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_NOT_RESUMABLE',
        message: 'The transfer cannot be resumed',
      );
    }

    final store = await _ensureTransferStore();
    if (!_isTransferPayloadPath(
      task.stagingPath,
      '${store.directoryPath}${Platform.pathSeparator}payloads',
    )) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_STAGING_SCOPE_INVALID',
        message: 'The transfer staging path is outside app storage',
      );
    }
    final payload = File(task.stagingPath);
    final payloadInfo = await payload.stat();
    if (payloadInfo.type != FileSystemEntityType.file ||
        payloadInfo.size != task.totalBytes) {
      task = task.copyWith(
        phase: transfer_core.TransferPhase.failed,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: 'UPLOAD_PAYLOAD_MISSING',
        errorMessage: '上传暂存文件不可用，无法安全继续。',
      );
      await _persistDurableTask(task);
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_PAYLOAD_MISSING',
        message: 'The private upload payload is unavailable',
      );
    }
    final actualPayloadSHA256 = await _hashUploadPayload(
      payload,
      cancellation: cancellation,
    );
    if (actualPayloadSHA256 != task.payloadSha256) {
      task = task.copyWith(
        phase: transfer_core.TransferPhase.failed,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: 'UPLOAD_PAYLOAD_CHANGED',
        errorMessage: '上传暂存文件校验失败，无法安全继续。',
      );
      await _persistDurableTask(task);
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_PAYLOAD_CHANGED',
        message: 'The private upload payload changed',
      );
    }

    task = task.copyWith(
      phase: transfer_core.TransferPhase.running,
      updatedAt: _nextTransferTimestamp(task),
      errorCode: null,
      errorMessage: null,
    );
    await _persistDurableTask(task);
    final files = _requireFiles();
    Future<Never> markUploadResultUnconfirmed() async {
      task = task.copyWith(
        phase: transfer_core.TransferPhase.resultUnconfirmed,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: 'UPLOAD_RESULT_UNCONFIRMED',
        errorMessage: '服务端已不再保留该上传会话，无法确认旧任务是否已经发布。请核对目标文件后移除记录。',
      );
      await _persistDurableTask(task);
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'UPLOAD_RESULT_UNCONFIRMED',
        message: 'The server no longer retains the upload session result',
      );
    }

    try {
      UploadSessionSnapshot session;
      if (task.uploadSessionId == null) {
        if (!task.uploadSessionCreateAttempted) {
          task = task.copyWith(
            uploadSessionCreateAttempted: true,
            updatedAt: _nextTransferTimestamp(task),
          );
          await _persistDurableTask(task);
          try {
            session = (await files.createUploadSession(
              logicalPath: task.remotePath,
              totalBytes: task.totalBytes,
              clientRequestId: task.id,
              cancelToken: cancellation,
            )).data;
          } on ApiException catch (error) {
            if (_isStructuredUploadSessionExpired(error)) {
              await markUploadResultUnconfirmed();
            }
            rethrow;
          }
        } else {
          try {
            session = (await files.lookupUploadSessionByClientRequestId(
              clientRequestId: task.id,
              logicalPath: task.remotePath,
              totalBytes: task.totalBytes,
              cancelToken: cancellation,
            )).data;
          } on ApiException catch (error) {
            if (_isStructuredUploadSessionGone(error)) {
              await markUploadResultUnconfirmed();
            }
            rethrow;
          }
        }
      } else {
        session = (await files.getUploadSessionStatus(
          sessionId: task.uploadSessionId!,
          cancelToken: cancellation,
        )).data;
      }
      _validateUploadSessionForTask(session, task);
      task = task.copyWith(
        uploadSessionId: session.id,
        uploadSessionCreateAttempted: true,
        uploadSessionExpiresAt: session.expiresAt,
        durableOffset: session.durableOffset,
        updatedAt: _nextTransferTimestamp(task),
      );
      await _persistDurableTask(task);

      while (true) {
        switch (session.state) {
          case UploadSessionState.committed:
            return _completeDurableUpload(task, session);
          case UploadSessionState.cancelled:
            task = task.copyWith(
              phase: transfer_core.TransferPhase.cancelled,
              durableOffset: session.durableOffset,
              uploadSessionExpiresAt: session.expiresAt,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: null,
              errorMessage: null,
            );
            await _persistDurableTask(task);
            throw const ApiException(
              kind: ApiFailureKind.local,
              code: 'UPLOAD_SESSION_CANCELLED',
              message: 'The upload session was cancelled',
            );
          case UploadSessionState.conflict:
            task = task.copyWith(
              phase: transfer_core.TransferPhase.failed,
              durableOffset: session.durableOffset,
              uploadSessionExpiresAt: session.expiresAt,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: 'UPLOAD_TARGET_CONFLICT',
              errorMessage: '目标文件已发生变化，上传未覆盖较新的内容。',
            );
            await _persistDurableTask(task);
            throw const ApiException(
              kind: ApiFailureKind.local,
              code: 'UPLOAD_TARGET_CONFLICT',
              message: 'The upload target changed',
            );
          case UploadSessionState.committing:
          case UploadSessionState.ready:
            try {
              session = (await files.commitUploadSession(
                sessionId: session.id,
                cancelToken: cancellation,
              )).data;
            } on ApiException catch (error) {
              if (error.statusCode != 409) {
                rethrow;
              }
              session = (await files.getUploadSessionStatus(
                sessionId: session.id,
                cancelToken: cancellation,
              )).data;
            }
            _validateUploadSessionForTask(session, task);
            task = task.copyWith(
              durableOffset: session.durableOffset,
              uploadSessionExpiresAt: session.expiresAt,
              updatedAt: _nextTransferTimestamp(task),
            );
            await _persistDurableTask(task);
          case UploadSessionState.uploading:
            final offset = session.durableOffset;
            final remaining = task.totalBytes - offset;
            if (remaining <= 0) {
              throw const FormatException(
                'Uploading session has no remaining payload',
              );
            }
            final length = remaining > maxUploadSessionChunkBytes
                ? maxUploadSessionChunkBytes
                : remaining;
            final chunkID =
                '${task.id}-${offset.toRadixString(16)}-'
                '${length.toRadixString(16)}';
            session = (await files.uploadSessionChunk(
              sessionId: session.id,
              sourcePath: task.stagingPath,
              offset: offset,
              length: length,
              chunkId: chunkID,
              cancelToken: cancellation,
              onProgress: (transferred, total) {
                _updateTransfer(
                  task.id,
                  status: TransferStatus.running,
                  transferred: transferred,
                  total: total,
                  expectedEpoch: epoch,
                );
              },
            )).data;
            _validateUploadSessionForTask(session, task);
            task = task.copyWith(
              durableOffset: session.durableOffset,
              uploadSessionExpiresAt: session.expiresAt,
              updatedAt: _nextTransferTimestamp(task),
            );
            await _persistDurableTask(task);
        }
      }
    } on Object catch (error) {
      if (task.uploadSessionId != null && _isUploadSessionGone(error)) {
        await markUploadResultUnconfirmed();
      }
      if (task.phase == transfer_core.TransferPhase.completed ||
          task.phase == transfer_core.TransferPhase.cancelled ||
          task.phase == transfer_core.TransferPhase.resultUnconfirmed ||
          (task.phase == transfer_core.TransferPhase.failed &&
              task.errorCode == 'UPLOAD_TARGET_CONFLICT')) {
        rethrow;
      }
      final phase = _uploadFailurePhase(error);
      final pauseReason =
          cancellation.isCancelled &&
              phase == transfer_core.TransferPhase.paused
          ? _transferPauseReasons[cancellation]
          : null;
      task = task.copyWith(
        phase: phase,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: pauseReason?.code ?? _uploadFailureCode(error, phase),
        errorMessage: pauseReason?.message ?? clientErrorMessage(error),
      );
      await _persistDurableTask(task);
      if (_isContextCurrent(epoch)) {
        await handleAuthenticationFailure(error, expectedEpoch: epoch);
      }
      rethrow;
    }
  }

  Future<String> _hashUploadPayload(
    File payload, {
    required CancelToken cancellation,
  }) async {
    final digestSink = _DigestCaptureSink();
    final hashSink = sha256.startChunkedConversion(digestSink);
    await for (final chunk in payload.openRead()) {
      if (cancellation.isCancelled) {
        throw const ApiException(
          kind: ApiFailureKind.cancelled,
          code: 'UPLOAD_HASH_CANCELLED',
          message: 'Upload verification was cancelled',
        );
      }
      hashSink.add(chunk);
    }
    hashSink.close();
    return digestSink.value.toString();
  }

  void _validateUploadSessionForTask(
    UploadSessionSnapshot session,
    transfer_core.TransferTask task,
  ) {
    if (session.path != task.remotePath ||
        session.totalBytes != task.totalBytes ||
        (task.uploadSessionId != null && session.id != task.uploadSessionId)) {
      throw const FormatException(
        'Upload session does not match the durable transfer',
      );
    }
  }

  Future<UploadSessionSnapshot> _completeDurableUpload(
    transfer_core.TransferTask task,
    UploadSessionSnapshot session,
  ) async {
    task = task.copyWith(
      phase: transfer_core.TransferPhase.completed,
      durableOffset: task.totalBytes,
      uploadSessionExpiresAt: session.expiresAt,
      updatedAt: _nextTransferTimestamp(task),
      errorCode: null,
      errorMessage: null,
    );
    await _persistDurableTask(task);
    try {
      final payload = File(task.stagingPath);
      if (await payload.exists()) {
        await payload.delete();
      }
    } on FileSystemException {
      if (!_disposed &&
          state.endpoint?.baseUrl == task.endpointBaseUrl &&
          state.user?.id == task.userId) {
        state = state.copyWith(notice: '文件已上传，但应用暂存文件未能立即清理。');
      }
    }
    return session;
  }

  Future<File> _downloadEphemeral({
    required FileEntry entry,
    required String destinationPath,
    required bool overwrite,
  }) async {
    final epoch = _contextEpoch;
    final files = _requireFiles();
    final id = _nextTransferId();
    final cancelToken = CancelToken();
    _transferCancellations[id] = cancelToken;
    _addTransfer(
      ClientTransfer(
        id: id,
        name: entry.name,
        direction: TransferDirection.download,
        status: TransferStatus.running,
        transferred: 0,
        total: entry.size,
      ),
      expectedEpoch: epoch,
    );
    try {
      final result = await files.downloadFile(
        logicalPath: entry.path,
        destinationPath: destinationPath,
        overwrite: overwrite,
        cancelToken: cancelToken,
        onProgress: (transferred, total) {
          _updateTransfer(
            id,
            transferred: transferred,
            total: total,
            expectedEpoch: epoch,
          );
        },
      );
      if (!_isContextCurrent(epoch)) {
        throw _operationSuperseded();
      }
      _updateTransfer(
        id,
        status: TransferStatus.completed,
        transferred: result.bytesWritten,
        total: result.bytesWritten,
        expectedEpoch: epoch,
      );
      if (result.warnings.isNotEmpty) {
        state = state.copyWith(notice: '文件已下载，但服务器报告了持久化警告。');
      }
      return result.file;
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        rethrow;
      }
      if (!await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        final cancelled =
            error is ApiException && error.kind == ApiFailureKind.cancelled;
        _updateTransfer(
          id,
          status: cancelled ? TransferStatus.cancelled : TransferStatus.failed,
          errorMessage: cancelled ? null : clientErrorMessage(error),
          expectedEpoch: epoch,
        );
      }
      rethrow;
    } finally {
      if (identical(_transferCancellations[id], cancelToken)) {
        _transferCancellations.remove(id);
      }
      _settledTransferCancellations.remove(cancelToken);
      _transferPauseReasons.remove(cancelToken);
    }
  }

  Future<File> _runDurableDownload(
    transfer_core.TransferTask initialTask, {
    bool overwrite = false,
  }) async {
    if (!_transferLeases.add(initialTask.id)) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_RUNNING',
        message: 'The transfer is already running',
      );
    }
    final cancellation = CancelToken();
    _transferCancellations[initialTask.id] = cancellation;
    try {
      return await _runDurableDownloadClaimed(
        initialTask,
        cancellation: cancellation,
        overwrite: overwrite,
      );
    } finally {
      if (identical(_transferCancellations[initialTask.id], cancellation)) {
        _transferCancellations.remove(initialTask.id);
      }
      _settledTransferCancellations.remove(cancellation);
      _transferPauseReasons.remove(cancellation);
      _transferLeases.remove(initialTask.id);
    }
  }

  Future<File> _runDurableDownloadClaimed(
    transfer_core.TransferTask initialTask, {
    required CancelToken cancellation,
    required bool overwrite,
  }) async {
    var task = initialTask;
    final epoch = _contextEpoch;
    final endpoint = state.endpoint;
    final user = state.user;
    if (endpoint?.baseUrl != task.endpointBaseUrl ||
        user?.id != task.userId ||
        state.stage != ClientStage.ready) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_SCOPE_MISMATCH',
        message: 'The transfer belongs to another server or account',
      );
    }
    final store = await _ensureTransferStore();
    if (!_isTransferPayloadPath(
      task.stagingPath,
      '${store.directoryPath}${Platform.pathSeparator}payloads',
    )) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_STAGING_SCOPE_INVALID',
        message: 'The transfer staging path is outside app storage',
      );
    }

    if (task.phase == transfer_core.TransferPhase.awaitingDestination) {
      if (task.destinationUri == null) {
        throw const ApiException(
          kind: ApiFailureKind.local,
          code: 'TRANSFER_DESTINATION_REQUIRED',
          message: 'A document destination must be selected',
        );
      }
      return _materializeUriDownload(task, cancellation: cancellation);
    }
    if (task.isTerminal && task.phase != transfer_core.TransferPhase.failed) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_NOT_RESUMABLE',
        message: 'The transfer cannot be resumed',
      );
    }

    final partial = File('${task.stagingPath}.part');
    var actualOffset = await partial.exists() ? await partial.length() : 0;
    if (actualOffset > task.totalBytes) {
      task = task.copyWith(
        phase: transfer_core.TransferPhase.failed,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: 'PARTIAL_FILE_INVALID',
        errorMessage: '断点文件大于源文件，无法安全继续。',
      );
      await _persistDurableTask(task);
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'DOWNLOAD_PART_INVALID',
        message: 'The partial download has an invalid length',
      );
    }
    if (actualOffset > 0 && task.validator == null) {
      await partial.delete();
      actualOffset = 0;
    }
    task = task.copyWith(
      phase: transfer_core.TransferPhase.running,
      durableOffset: actualOffset,
      updatedAt: _nextTransferTimestamp(task),
      errorCode: null,
      errorMessage: null,
    );
    await _persistDurableTask(task);

    final files = _requireFiles();
    try {
      final result = await files.downloadFile(
        logicalPath: task.remotePath,
        destinationPath: task.destinationPath ?? task.stagingPath,
        stagingPath: partial.path,
        resumeValidator: task.validator,
        expectedTotalBytes: task.totalBytes,
        overwrite: task.destinationPath == null ? true : overwrite,
        preservePartialOnFailure: true,
        cancelToken: cancellation,
        onProgress: (transferred, total) {
          _updateTransfer(
            task.id,
            transferred: transferred,
            total: total,
            expectedEpoch: epoch,
          );
        },
        onCheckpoint: (checkpoint) async {
          task = task.copyWith(
            validator: checkpoint.validator,
            durableOffset: checkpoint.durableOffset,
            updatedAt: _nextTransferTimestamp(task),
          );
          await _persistDurableTask(task);
        },
      );
      task = task.copyWith(
        phase: task.destinationPath != null
            ? transfer_core.TransferPhase.completed
            : transfer_core.TransferPhase.awaitingDestination,
        validator: result.validator,
        durableOffset: result.totalBytes,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: null,
        errorMessage: null,
      );
      await _persistDurableTask(task);
      if (result.warnings.isNotEmpty && _isContextCurrent(epoch)) {
        state = state.copyWith(notice: '文件已下载，但服务器报告了持久化警告。');
      }
      if (task.destinationUri != null) {
        return _materializeUriDownload(task, cancellation: cancellation);
      }
      return result.file;
    } on Object catch (error) {
      if (task.phase == transfer_core.TransferPhase.awaitingDestination) {
        rethrow;
      }
      final phase = _downloadFailurePhase(error);
      final pauseReason =
          cancellation.isCancelled &&
              phase == transfer_core.TransferPhase.paused
          ? _transferPauseReasons[cancellation]
          : null;
      task = task.copyWith(
        phase: phase,
        durableOffset: await partial.exists() ? await partial.length() : 0,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: pauseReason?.code ?? _downloadFailureCode(error, phase),
        errorMessage: pauseReason?.message ?? clientErrorMessage(error),
      );
      await _persistDurableTask(task);
      if (_isContextCurrent(epoch)) {
        await handleAuthenticationFailure(error, expectedEpoch: epoch);
      }
      rethrow;
    }
  }

  Future<File> _materializeUriDownload(
    transfer_core.TransferTask initialTask, {
    required CancelToken cancellation,
  }) async {
    var task = initialTask;
    final uri = task.destinationUri;
    if (uri == null ||
        task.phase != transfer_core.TransferPhase.awaitingDestination) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_DESTINATION_INVALID',
        message: 'The transfer has no pending document destination',
      );
    }
    final payload = File(task.stagingPath);
    if (!await payload.exists() || await payload.length() != task.totalBytes) {
      task = task.copyWith(
        phase: transfer_core.TransferPhase.failed,
        updatedAt: _nextTransferTimestamp(task),
        errorCode: 'STAGED_DOWNLOAD_MISSING',
        errorMessage: '已完成的下载暂存文件不可用。',
      );
      await _persistDurableTask(task);
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'STAGED_DOWNLOAD_MISSING',
        message: 'The completed download staging file is unavailable',
      );
    }
    var operationActive = true;
    unawaited(
      cancellation.whenCancel.then((_) async {
        if (!operationActive) {
          return;
        }
        try {
          await _contentUriMaterializationCanceller(task.id);
        } on Object {
          // The copy future remains authoritative for cancellation failure.
        }
      }),
    );
    _updateTransfer(
      task.id,
      status: TransferStatus.running,
      transferred: 0,
      total: task.totalBytes,
    );
    try {
      await _contentUriMaterializer(
        sourcePath: payload.path,
        destinationUri: uri,
        operationId: task.id,
        onProgress: (transferred, total) {
          _updateTransfer(
            task.id,
            status: TransferStatus.running,
            transferred: transferred,
            total: total ?? task.totalBytes,
          );
        },
      );
    } on Object catch (error) {
      final pauseReason = cancellation.isCancelled
          ? _transferPauseReasons[cancellation]
          : null;
      task = task.copyWith(
        updatedAt: _nextTransferTimestamp(task),
        destinationUri: cancellation.isCancelled ? null : task.destinationUri,
        errorCode: pauseReason?.code ?? 'DESTINATION_WRITE_FAILED',
        errorMessage:
            pauseReason?.destinationMessage ??
            (cancellation.isCancelled
                ? '保存已暂停，可重新选择位置。'
                : clientErrorMessage(error)),
      );
      await _persistDurableTask(task);
      rethrow;
    } finally {
      operationActive = false;
    }
    task = task.copyWith(
      phase: transfer_core.TransferPhase.completed,
      updatedAt: _nextTransferTimestamp(task),
      errorCode: null,
      errorMessage: null,
    );
    await _persistDurableTask(task);
    try {
      await payload.delete();
    } on FileSystemException {
      if (!_disposed &&
          state.endpoint?.baseUrl == task.endpointBaseUrl &&
          state.user?.id == task.userId) {
        state = state.copyWith(notice: '文件已保存，但应用暂存文件未能立即清理。');
      }
    }
    return payload;
  }

  void _ensureForegroundTransferStart({
    required int expectedEpoch,
    required int expectedForegroundGeneration,
    required CancelToken cancellation,
  }) {
    if (!_isContextCurrent(expectedEpoch)) {
      throw _operationSuperseded();
    }
    final pauseReason = _transferPauseReasons[cancellation];
    if (expectedForegroundGeneration != _foregroundTransferGeneration ||
        pauseReason?.code == _appBackgroundedTransferPause.code) {
      throw _appBackgroundedTransferException;
    }
    if (cancellation.isCancelled) {
      throw const ApiException(
        kind: ApiFailureKind.cancelled,
        code: 'UPLOAD_PREPARATION_CANCELLED',
        message: 'Upload preparation was cancelled',
      );
    }
  }

  Future<transfer_core.TransferTask>
  _pauseDurableTransferBeforeStartIfBackgrounded(
    transfer_core.TransferTask task, {
    required int expectedForegroundGeneration,
  }) async {
    if (expectedForegroundGeneration == _foregroundTransferGeneration ||
        _isAppBackgroundedTask(task)) {
      return task;
    }
    final awaitingDestination =
        task.phase == transfer_core.TransferPhase.awaitingDestination;
    final paused = task.copyWith(
      phase: awaitingDestination
          ? transfer_core.TransferPhase.awaitingDestination
          : transfer_core.TransferPhase.paused,
      destinationUri: awaitingDestination ? null : task.destinationUri,
      updatedAt: _nextTransferTimestamp(task),
      errorCode: _appBackgroundedTransferPause.code,
      errorMessage: awaitingDestination
          ? _appBackgroundedTransferPause.destinationMessage
          : _appBackgroundedTransferPause.message,
    );
    await _persistDurableTask(paused);
    return paused;
  }

  bool _isAppBackgroundedTask(transfer_core.TransferTask task) =>
      task.errorCode == _appBackgroundedTransferPause.code;

  void pauseTransfer(String id) {
    final token = _transferCancellations[id];
    if (token == null ||
        token.isCancelled ||
        _settledTransferCancellations.contains(token)) {
      return;
    }
    token.cancel('Cancelled by the user');
    if (_durableTransfers.containsKey(id)) {
      _updateTransfer(id, status: TransferStatus.paused);
    } else {
      _updateTransfer(id, status: TransferStatus.cancelled);
    }
  }

  void cancelTransfer(String id) => pauseTransfer(id);

  Future<int> pauseActiveTransfersForAppBackground() async {
    if (_disposed || state.stage != ClientStage.ready) {
      return 0;
    }
    _foregroundTransferGeneration++;
    final versionDownloads = _versionDownloadCancellations
        .where((cancellation) => !cancellation.isCancelled)
        .toList(growable: false);
    for (final cancellation in versionDownloads) {
      cancellation.cancel('App entered the background');
    }
    final active = _transferCancellations.entries
        .where(
          (entry) =>
              !entry.value.isCancelled &&
              !_settledTransferCancellations.contains(entry.value),
        )
        .toList(growable: false);
    for (final entry in active) {
      final id = entry.key;
      final task = _durableTransfers[id];
      _transferPauseReasons[entry.value] = _appBackgroundedTransferPause;
      entry.value.cancel(_appBackgroundedTransferPause.cancellationMessage);
      if (task == null) {
        _updateTransfer(id, status: TransferStatus.cancelled);
        continue;
      }
      _updateTransfer(
        id,
        status: task.phase == transfer_core.TransferPhase.awaitingDestination
            ? TransferStatus.awaitingDestination
            : TransferStatus.paused,
        errorMessage:
            task.phase == transfer_core.TransferPhase.awaitingDestination
            ? _appBackgroundedTransferPause.destinationMessage
            : _appBackgroundedTransferPause.message,
      );
    }
    return active.length + versionDownloads.length;
  }

  Future<void> resumeTransfer(String id) async {
    final task = _durableTransfers[id];
    if (task == null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_NOT_FOUND',
        message: 'The transfer record is unavailable',
      );
    }
    if (_transferLeases.contains(id)) {
      return;
    }
    if (task.direction == transfer_core.TransferDirection.upload) {
      final epoch = _contextEpoch;
      final result = await _runDurableUpload(task);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      final parent = _logicalParentPath(task.remotePath);
      if (state.currentPath == parent) {
        try {
          await loadDirectory(parent);
        } on Object {
          if (_isContextCurrent(epoch)) {
            state = state.copyWith(
              isBusy: false,
              errorMessage: null,
              notice: result.persistenceWarning
                  ? '文件已确认上传，但服务器报告了持久化警告，且目录刷新失败。请手动刷新确认最新目录。'
                  : '文件已确认上传，但目录刷新失败。请手动刷新确认最新目录。',
            );
          }
          return;
        }
      }
      if (_isContextCurrent(epoch) && result.persistenceWarning) {
        state = state.copyWith(notice: '文件已上传，但服务器报告了持久化警告。');
      }
      return;
    }
    await _runDurableDownload(task);
  }

  Future<void> removeTransfer(String id) async {
    if (_transferLeases.contains(id)) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_RUNNING',
        message: 'Pause the transfer before removing it',
      );
    }
    final task = _durableTransfers[id];
    if (task == null) {
      state = state.copyWith(
        transfers: List<ClientTransfer>.unmodifiable(
          state.transfers.where((transfer) => transfer.id != id),
        ),
      );
      return;
    }
    if (state.endpoint?.baseUrl != task.endpointBaseUrl ||
        state.user?.id != task.userId) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_SCOPE_MISMATCH',
        message: 'The transfer belongs to another server or account',
      );
    }
    if (task.direction == transfer_core.TransferDirection.upload &&
        task.uploadSessionId != null &&
        task.phase != transfer_core.TransferPhase.completed &&
        task.phase != transfer_core.TransferPhase.cancelled &&
        task.phase != transfer_core.TransferPhase.resultUnconfirmed &&
        task.errorCode != 'UPLOAD_TARGET_CONFLICT') {
      try {
        final result = await _requireFiles().cancelUploadSession(
          sessionId: task.uploadSessionId!,
        );
        if (result.data.state != UploadSessionState.cancelled) {
          throw const ApiException(
            kind: ApiFailureKind.invalidResponse,
            code: 'UPLOAD_CANCEL_UNCONFIRMED',
            message: 'The server did not confirm upload cancellation',
          );
        }
      } on ApiException catch (error) {
        if (!_isUploadSessionGone(error)) {
          rethrow;
        }
      }
    }
    final store = await _ensureTransferStore();
    final payloadDirectory =
        '${store.directoryPath}${Platform.pathSeparator}payloads';
    if (_isTransferPayloadPath(task.stagingPath, payloadDirectory)) {
      for (final path in <String>[
        task.stagingPath,
        '${task.stagingPath}.part',
      ]) {
        final file = File(path);
        if (await file.exists()) {
          await file.delete();
        }
      }
    }
    await store.remove(id);
    _durableTransfers.remove(id);
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(
        state.transfers.where((transfer) => transfer.id != id),
      ),
    );
  }

  @visibleForTesting
  Future<bool> handleAuthenticationFailure(
    Object error, {
    ServerEndpoint? endpoint,
    ServerProbe? probe,
    int? expectedEpoch,
  }) async {
    if (!_isTerminalAuthenticationFailure(error)) {
      return false;
    }
    final epoch = expectedEpoch ?? _contextEpoch;
    if (!_isContextCurrent(epoch)) {
      return true;
    }
    if (state.stage == ClientStage.needsLogin && state.user == null) {
      return true;
    }
    await _resetExpiredSession(
      endpoint: endpoint,
      probe: probe,
      expectedEpoch: epoch,
    );
    return true;
  }

  Future<void> _resetExpiredSession({
    ServerEndpoint? endpoint,
    ServerProbe? probe,
    int? expectedEpoch,
  }) async {
    final epoch = expectedEpoch ?? _contextEpoch;
    if (!_isContextCurrent(epoch)) {
      return;
    }
    final nextEndpoint = endpoint ?? state.endpoint;
    final nextProbe = probe ?? state.probe;
    _invalidateContext();
    state = ClientState(
      stage: ClientStage.needsLogin,
      endpoint: nextEndpoint,
      probe: nextProbe,
      errorMessage: '登录状态已失效，请重新登录。',
    );
    try {
      await ref.read(authSessionStoreProvider).takeAndClear();
    } on Object {
      // The in-memory state must still stop using credentials that the server
      // has rejected, even when local secure storage is temporarily unavailable.
    }
  }

  Future<bool> _fenceAndClearSessionForEpoch(int epoch) async {
    while (_isContextCurrent(epoch)) {
      final snapshot = await _sessionStore.snapshot();
      if (!_isContextCurrent(epoch)) {
        return false;
      }
      final cleared = await _sessionStore.takeAndClearIfRevision(
        snapshot.revision,
      );
      if (!_isContextCurrent(epoch)) {
        return false;
      }
      if (cleared != null) {
        return true;
      }
    }
    return false;
  }

  Future<void> _terminatePasswordSession({
    required int expectedEpoch,
    required ServerEndpoint endpoint,
    String? notice,
    String? errorMessage,
    required String storageFailureMessage,
  }) async {
    if (!_isContextCurrent(expectedEpoch)) {
      return;
    }
    final probe = state.probe;
    final cleanupEpoch = _invalidateContext();
    state = ClientState(
      stage: ClientStage.booting,
      endpoint: endpoint,
      probe: probe,
      isBusy: true,
    );

    Object? clearFailure;
    try {
      await _fenceAndClearSessionForEpoch(cleanupEpoch);
    } on Object catch (error) {
      clearFailure = error;
    }
    if (!_isContextCurrent(cleanupEpoch)) {
      return;
    }
    state = ClientState(
      stage: ClientStage.needsLogin,
      endpoint: endpoint,
      probe: probe,
      notice: clearFailure == null ? notice : null,
      errorMessage: clearFailure == null ? errorMessage : storageFailureMessage,
    );
  }

  void clearMessage() {
    state = state.copyWith(errorMessage: null, notice: null);
  }

  void _cancelAllTransfers() {
    for (final token in _transferCancellations.values.toList(growable: false)) {
      if (!token.isCancelled) {
        token.cancel('Client session ended');
      }
    }
    _transferCancellations.clear();
  }

  int _configure(ServerEndpoint endpoint) {
    final epoch = _invalidateContext();
    final client = _apiClientFactory(endpoint, _sessionStore);
    _client = client;
    _auth = AuthApi(client);
    _files = FilesApi(client);
    _search = SearchApi(client);
    _system = SystemApi(client);
    _trash = TrashApi(client);
    return epoch;
  }

  int _invalidateContext() {
    _contextEpoch++;
    _fileMutationLease = null;
    _fileMutationReconciliationPaths.clear();
    _directorySequence++;
    _searchSequence++;
    _searchTargetSequence++;
    _trashSequence++;
    _directoryCancellation?.cancel('Client context changed');
    _directoryCancellation = null;
    _overviewCancellation?.cancel('Client context changed');
    _overviewCancellation = null;
    _searchCancellation?.cancel('Client context changed');
    _searchCancellation = null;
    _searchTargetCancellation?.cancel('Client context changed');
    _searchTargetCancellation = null;
    _trashReadCancellation?.cancel('Client context changed');
    _trashReadCancellation = null;
    _trashMutationCancellation?.cancel('Client context changed');
    _trashMutationCancellation = null;
    _versionRestoreCancellation?.cancel('Client context changed');
    _versionRestoreCancellation = null;
    for (final cancellation in _versionDownloadCancellations.toList(
      growable: false,
    )) {
      if (!cancellation.isCancelled) {
        cancellation.cancel('Client context changed');
      }
    }
    _versionDownloadCancellations.clear();
    _settledTransferCancellations.clear();
    _cancelAllTransfers();
    _client?.close();
    _client = null;
    _auth = null;
    _files = null;
    _search = null;
    _system = null;
    _trash = null;
    return _contextEpoch;
  }

  void _disposeController() {
    if (_disposed) {
      return;
    }
    _disposed = true;
    _invalidateContext();
  }

  bool _isContextCurrent(int epoch) => !_disposed && epoch == _contextEpoch;

  bool _isDirectoryRequestCurrent(
    int epoch,
    int sequence,
    CancelToken cancellation,
  ) =>
      _isContextCurrent(epoch) &&
      sequence == _directorySequence &&
      identical(_directoryCancellation, cancellation);

  bool _isTrashRequestCurrent(
    int epoch,
    int sequence,
    CancelToken cancellation,
  ) =>
      _isContextCurrent(epoch) &&
      sequence == _trashSequence &&
      identical(_trashReadCancellation, cancellation);

  bool _isSearchRequestCurrent(
    int epoch,
    int sequence,
    CancelToken cancellation,
  ) =>
      _isContextCurrent(epoch) &&
      sequence == _searchSequence &&
      identical(_searchCancellation, cancellation);

  bool _isSearchTargetRequestCurrent(
    int epoch,
    int sequence,
    CancelToken cancellation,
  ) =>
      _isContextCurrent(epoch) &&
      sequence == _searchTargetSequence &&
      identical(_searchTargetCancellation, cancellation);

  bool _isTrashMutationCurrent(int epoch, CancelToken cancellation) =>
      _isContextCurrent(epoch) &&
      identical(_trashMutationCancellation, cancellation);

  void _beginTrashMutation(CancelToken cancellation) {
    ++_trashSequence;
    _trashReadCancellation?.cancel('Superseded by trash mutation');
    _trashReadCancellation = null;
    _trashMutationCancellation = cancellation;
  }

  Future<TrashListing?> _reconcileTrashAfterUnconfirmed(int epoch) async {
    if (!_isContextCurrent(epoch)) {
      return null;
    }
    final sequence = ++_trashSequence;
    _trashReadCancellation?.cancel('Superseded trash reconciliation');
    final cancellation = CancelToken();
    _trashReadCancellation = cancellation;
    final trash = _trash;
    if (trash == null) {
      return null;
    }
    try {
      final response = await trash.list(cancelToken: cancellation);
      if (!_isTrashRequestCurrent(epoch, sequence, cancellation)) {
        return null;
      }
      state = state.copyWith(
        trash: response.data,
        isTrashBusy: false,
        trashReconciliationRequired: true,
        trashErrorMessage:
            '操作结果未获确认，回收站已刷新，但无法证明项目已恢复或永久删除。'
            '请核对目标目录或活动记录，并再次刷新回收站后继续。',
      );
      return response.data;
    } on Object catch (error) {
      if (!_isTrashRequestCurrent(epoch, sequence, cancellation) ||
          _isCancellation(error)) {
        return null;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        return null;
      }
      state = state.copyWith(
        isTrashBusy: false,
        trashReconciliationRequired: true,
        trashErrorMessage: '无法核对操作结果。刷新回收站成功前，已暂停后续恢复和永久删除。',
      );
      return null;
    } finally {
      if (identical(_trashReadCancellation, cancellation)) {
        _trashReadCancellation = null;
      }
    }
  }

  void _ensureTrashMutationAllowed() {
    if (state.stage != ClientStage.ready || state.user == null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    if (state.user!.role == 'guest') {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'READ_ONLY_ACCOUNT',
        message: 'This account cannot modify trash',
      );
    }
    if (state.trashReconciliationRequired) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRASH_RECONCILIATION_REQUIRED',
        message: 'Refresh trash before another destructive action',
      );
    }
    final activeMutation = _trashMutationCancellation;
    if (activeMutation != null && !activeMutation.isCancelled) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRASH_MUTATION_IN_PROGRESS',
        message: 'Another trash mutation is still running',
      );
    }
  }

  bool _requiresTrashReconciliation(Object error) {
    if (error is! ApiException) {
      return false;
    }
    final statusCode = error.statusCode;
    return error.isUnconfirmedMutation ||
        (statusCode != null && statusCode >= 500 && statusCode < 600);
  }

  bool _requiresFileMutationReconciliation(Object error) {
    if (error is! ApiException) {
      return true;
    }
    final statusCode = error.statusCode;
    return error.isUnconfirmedMutation ||
        (statusCode != null && statusCode >= 500 && statusCode < 600);
  }

  _VersionTarget _requireVersionTarget(FileEntry entry) {
    if (state.stage != ClientStage.ready || state.user == null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    if (entry.isDirectory ||
        !entry.versioned ||
        !entry.capabilities.concreteRead) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'VERSION_TARGET_INVALID',
        message: 'Version history requires a readable regular file',
      );
    }
    final path = normalizeLogicalPath(entry.path, allowRoot: false);
    if (path != entry.path) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'VERSION_TARGET_INVALID',
        message: 'The version target path is not canonical',
      );
    }
    final currentHash = entry.contentHash;
    if (currentHash != null &&
        !_lowercaseBlake3DigestPattern.hasMatch(currentHash)) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'VERSION_IDENTITY_INVALID',
        message: 'The current file identity is invalid',
      );
    }
    return _VersionTarget(
      path: path,
      parentPath: _logicalParentPath(path),
      openedCurrentHash: currentHash,
    );
  }

  void _requireVersionForTarget(FileVersion version, _VersionTarget target) {
    if (version.path != target.path) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'VERSION_TARGET_MISMATCH',
        message: 'The selected version belongs to a different file',
      );
    }
  }

  void _ensureVersionRestoreAllowed() {
    if (state.stage != ClientStage.ready || state.user == null) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'AUTH_SESSION_MISSING',
        message: 'Sign in is required',
      );
    }
    if (state.user!.role != 'admin') {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'ADMIN_REQUIRED',
        message: 'Administrator access is required to restore versions',
      );
    }
  }

  void _requireMatchingCurrentVersion(
    FileVersionHistory history, {
    required String expectedHash,
  }) {
    if (history.current.hash != expectedHash) {
      throw _versionTargetChanged();
    }
  }

  FileEntry? _findFileEntry(DirectoryListing listing, String path) {
    for (final entry in listing.entries) {
      if (entry.path == path) {
        return entry;
      }
    }
    return null;
  }

  Future<void> _serializePreferenceMutation(Future<void> Function() operation) {
    final result = Completer<void>();
    _preferenceMutationTail = _preferenceMutationTail.then((_) async {
      try {
        await operation();
        result.complete();
      } catch (error, stackTrace) {
        result.completeError(error, stackTrace);
      }
    });
    _preferenceMutationTail = _preferenceMutationTail.catchError((Object _) {});
    return result.future;
  }

  FilesApi _requireFiles() {
    final files = _files;
    if (files == null) {
      throw StateError('The server is not configured');
    }
    return files;
  }

  SearchApi _requireSearch() {
    final search = _search;
    if (search == null) {
      throw StateError('The server is not configured');
    }
    return search;
  }

  TrashApi _requireTrash() {
    final trash = _trash;
    if (trash == null) {
      throw StateError('The server is not configured');
    }
    return trash;
  }

  String _nextTransferId() {
    _transferSequence++;
    return '${DateTime.now().microsecondsSinceEpoch}-$_transferSequence';
  }

  Future<transfer_core.FileTransferStore> _ensureTransferStore() async {
    final existing = _transferStoreFuture;
    if (existing != null) {
      return existing;
    }
    final created = _transferStoreFactory();
    _transferStoreFuture = created;
    try {
      return await created;
    } on Object {
      if (identical(_transferStoreFuture, created)) {
        _transferStoreFuture = null;
      }
      rethrow;
    }
  }

  Future<void> _pruneCompletedTransferHistory(
    transfer_core.FileTransferStore store,
  ) async {
    final snapshot = await store.load();
    final terminal =
        snapshot.tasks
            .where(
              (task) =>
                  task.phase == transfer_core.TransferPhase.completed ||
                  task.phase == transfer_core.TransferPhase.cancelled,
            )
            .toList(growable: false)
          ..sort((left, right) => right.updatedAt.compareTo(left.updatedAt));
    if (terminal.length <= 200) {
      return;
    }
    final payloadDirectory =
        '${store.directoryPath}${Platform.pathSeparator}payloads';
    final removable = <String>{};
    for (final task in terminal.skip(200)) {
      if (!_isTransferPayloadPath(task.stagingPath, payloadDirectory)) {
        continue;
      }
      var cleanupSucceeded = true;
      for (final path in <String>[
        task.stagingPath,
        '${task.stagingPath}.part',
      ]) {
        try {
          final file = File(path);
          if (await file.exists()) {
            await file.delete();
          }
        } on FileSystemException {
          cleanupSucceeded = false;
        }
      }
      if (cleanupSucceeded) {
        removable.add(task.id);
      }
    }
    if (removable.isEmpty) {
      return;
    }
    await store.retainWhere((task) => !removable.contains(task.id));
    _durableTransfers.removeWhere((id, _) => removable.contains(id));
    if (!_disposed) {
      state = state.copyWith(
        transfers: List<ClientTransfer>.unmodifiable(
          state.transfers.where((transfer) => !removable.contains(transfer.id)),
        ),
      );
    }
  }

  Future<void> _restoreDurableTransfers({required int expectedEpoch}) async {
    if (!_isContextCurrent(expectedEpoch)) {
      return;
    }
    final endpoint = state.endpoint;
    final user = state.user;
    if (endpoint == null || user == null) {
      return;
    }
    try {
      final store = await _ensureTransferStore();
      final snapshot = await store.load();
      if (!_isContextCurrent(expectedEpoch)) {
        return;
      }
      final payloadDirectory = Directory(
        '${store.directoryPath}${Platform.pathSeparator}payloads',
      );
      await payloadDirectory.create(recursive: true);
      await _cleanupOrphanUploadPayloads(
        payloadDirectory,
        tasks: snapshot.tasks,
      );
      await _cleanupConfirmedUploadPayloads(
        payloadDirectory,
        tasks: snapshot.tasks,
      );
      final restored = <transfer_core.TransferTask>[];
      for (var task in snapshot.tasks) {
        if (task.endpointBaseUrl != endpoint.baseUrl ||
            task.userId != user.id) {
          continue;
        }
        var changed = false;
        if (!_isTransferPayloadPath(task.stagingPath, payloadDirectory.path)) {
          task = task.copyWith(
            phase: transfer_core.TransferPhase.failed,
            updatedAt: _nextTransferTimestamp(task),
            errorCode: 'STAGING_SCOPE_INVALID',
            errorMessage: '传输暂存路径不在应用私有目录中。',
          );
          changed = true;
        } else if (task.direction == transfer_core.TransferDirection.download &&
            task.phase == transfer_core.TransferPhase.running &&
            task.durableOffset == task.totalBytes &&
            !await File('${task.stagingPath}.part').exists()) {
          final payload = File(task.stagingPath);
          if (task.destinationPath == null &&
              await payload.exists() &&
              await payload.length() == task.totalBytes) {
            task = task.copyWith(
              phase: transfer_core.TransferPhase.awaitingDestination,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: null,
              errorMessage: null,
            );
          } else {
            task = task.copyWith(
              phase: transfer_core.TransferPhase.resultUnconfirmed,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: 'DOWNLOAD_RESULT_UNCONFIRMED',
              errorMessage: '下载可能已写入目标位置，请核对后移除记录。',
            );
          }
          changed = true;
        } else if (task.phase == transfer_core.TransferPhase.running ||
            task.phase == transfer_core.TransferPhase.queued) {
          task = task.copyWith(
            phase: transfer_core.TransferPhase.paused,
            updatedAt: _nextTransferTimestamp(task),
            errorCode: 'CLIENT_RESTARTED',
            errorMessage: '客户端重启后，传输已暂停，可继续。',
          );
          changed = true;
        }

        if (task.direction == transfer_core.TransferDirection.download &&
            task.phase == transfer_core.TransferPhase.awaitingDestination &&
            task.destinationUri != null) {
          task = task.copyWith(
            destinationUri: null,
            updatedAt: _nextTransferTimestamp(task),
            errorCode: 'DESTINATION_SELECTION_REQUIRED',
            errorMessage: '客户端重启后需要重新选择保存位置。',
          );
          changed = true;
        }
        if (task.direction == transfer_core.TransferDirection.download &&
            _isTransferPayloadPath(task.stagingPath, payloadDirectory.path) &&
            !task.isTerminal &&
            task.phase != transfer_core.TransferPhase.awaitingDestination) {
          final partial = File('${task.stagingPath}.part');
          final actualOffset = await partial.exists()
              ? await partial.length()
              : 0;
          if (actualOffset > task.totalBytes) {
            task = task.copyWith(
              phase: transfer_core.TransferPhase.failed,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: 'PARTIAL_FILE_INVALID',
              errorMessage: '断点文件大于源文件，无法安全继续。',
            );
            changed = true;
          } else if (actualOffset != task.durableOffset) {
            task = task.copyWith(
              durableOffset: actualOffset,
              updatedAt: _nextTransferTimestamp(task),
            );
            changed = true;
          }
        }
        if (task.phase == transfer_core.TransferPhase.awaitingDestination) {
          final payload = File(task.stagingPath);
          if (!await payload.exists() ||
              await payload.length() != task.totalBytes) {
            task = task.copyWith(
              phase: transfer_core.TransferPhase.failed,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: 'STAGED_DOWNLOAD_MISSING',
              errorMessage: '已完成的下载暂存文件不可用。',
            );
            changed = true;
          }
        }
        if (task.direction == transfer_core.TransferDirection.upload &&
            !task.isTerminal) {
          final payload = File(task.stagingPath);
          if (!await payload.exists() ||
              await payload.length() != task.totalBytes) {
            task = task.copyWith(
              phase: transfer_core.TransferPhase.failed,
              updatedAt: _nextTransferTimestamp(task),
              errorCode: 'UPLOAD_PAYLOAD_MISSING',
              errorMessage: '上传暂存文件不可用，无法安全继续。',
            );
            changed = true;
          }
        }
        if (changed) {
          await store.upsert(task);
        }
        restored.add(task);
      }
      if (!_isContextCurrent(expectedEpoch)) {
        return;
      }
      restored.sort((left, right) => right.updatedAt.compareTo(left.updatedAt));
      _durableTransfers
        ..clear()
        ..addEntries(restored.map((task) => MapEntry(task.id, task)));
      state = state.copyWith(
        transfers: List<ClientTransfer>.unmodifiable(
          restored.take(100).map(_clientTransferFromTask),
        ),
      );
    } on Object {
      if (_isContextCurrent(expectedEpoch)) {
        state = state.copyWith(notice: '传输记录暂时无法读取。现有传输未自动恢复，请检查应用存储。');
      }
    }
  }

  Future<void> _cleanupOrphanUploadPayloads(
    Directory payloadDirectory, {
    required List<transfer_core.TransferTask> tasks,
  }) async {
    final referenced = <String>{
      for (final task in tasks) task.stagingPath,
      for (final task in tasks) '${task.stagingPath}.part',
    };
    await for (final entity in payloadDirectory.list(followLinks: false)) {
      if (referenced.contains(entity.path) ||
          (!entity.path.endsWith('.upload') &&
              !entity.path.endsWith('.upload.part'))) {
        continue;
      }
      final type = await FileSystemEntity.type(entity.path, followLinks: false);
      if (type != FileSystemEntityType.file ||
          !_isTransferPayloadPath(entity.path, payloadDirectory.path)) {
        continue;
      }
      try {
        await File(entity.path).delete();
      } on FileSystemException {
        // A later startup can retry an unreferenced app-private upload file.
      }
    }
  }

  Future<void> _cleanupConfirmedUploadPayloads(
    Directory payloadDirectory, {
    required List<transfer_core.TransferTask> tasks,
  }) async {
    for (final task in tasks) {
      if (task.direction != transfer_core.TransferDirection.upload ||
          (task.phase != transfer_core.TransferPhase.completed &&
              task.phase != transfer_core.TransferPhase.cancelled) ||
          !_isTransferPayloadPath(task.stagingPath, payloadDirectory.path)) {
        continue;
      }
      for (final path in <String>[
        task.stagingPath,
        '${task.stagingPath}.part',
      ]) {
        try {
          final type = await FileSystemEntity.type(path, followLinks: false);
          if (type == FileSystemEntityType.file) {
            await File(path).delete();
          }
        } on FileSystemException {
          // A later startup can retry cleanup after the server result is final.
        }
      }
    }
  }

  Future<void> _persistDurableTask(transfer_core.TransferTask task) async {
    final store = await _ensureTransferStore();
    if (!_isTransferPayloadPath(
      task.stagingPath,
      '${store.directoryPath}${Platform.pathSeparator}payloads',
    )) {
      throw const ApiException(
        kind: ApiFailureKind.local,
        code: 'TRANSFER_STAGING_SCOPE_INVALID',
        message: 'The transfer staging path is outside app storage',
      );
    }
    await store.upsert(task);
    _durableTransfers[task.id] = task;
    final cancellation = _transferCancellations[task.id];
    if (cancellation != null) {
      if (task.isTerminal) {
        _settledTransferCancellations.add(cancellation);
      } else {
        _settledTransferCancellations.remove(cancellation);
      }
    }
    if (!_disposed &&
        state.endpoint?.baseUrl == task.endpointBaseUrl &&
        state.user?.id == task.userId) {
      _replaceTransfer(_clientTransferFromTask(task));
    }
  }

  ClientTransfer _clientTransferFromTask(transfer_core.TransferTask task) {
    final status = switch (task.phase) {
      transfer_core.TransferPhase.queued => TransferStatus.queued,
      transfer_core.TransferPhase.running => TransferStatus.running,
      transfer_core.TransferPhase.paused => TransferStatus.paused,
      transfer_core.TransferPhase.awaitingAuth => TransferStatus.awaitingAuth,
      transfer_core.TransferPhase.awaitingDestination =>
        TransferStatus.awaitingDestination,
      transfer_core.TransferPhase.resultUnconfirmed =>
        TransferStatus.resultUnconfirmed,
      transfer_core.TransferPhase.completed => TransferStatus.completed,
      transfer_core.TransferPhase.failed => TransferStatus.failed,
      transfer_core.TransferPhase.cancelled => TransferStatus.cancelled,
    };
    return ClientTransfer(
      id: task.id,
      name: task.displayName,
      direction: task.direction == transfer_core.TransferDirection.download
          ? TransferDirection.download
          : TransferDirection.upload,
      status: status,
      transferred: task.durableOffset,
      total: task.totalBytes,
      canRetry: _canRetryDurableTransfer(task),
      errorMessage: task.errorMessage,
    );
  }

  void _replaceTransfer(ClientTransfer transfer) {
    final existingIndex = state.transfers.indexWhere(
      (candidate) => candidate.id == transfer.id,
    );
    final next = state.transfers.toList(growable: true);
    if (existingIndex >= 0) {
      next[existingIndex] = transfer;
    } else {
      next.insert(0, transfer);
    }
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(next.take(100)),
    );
  }

  void _addTransfer(ClientTransfer transfer, {int? expectedEpoch}) {
    if (expectedEpoch != null && !_isContextCurrent(expectedEpoch)) {
      return;
    }
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(<ClientTransfer>[
        transfer,
        ...state.transfers.take(99),
      ]),
    );
  }

  void _updateTransfer(
    String id, {
    TransferStatus? status,
    int? transferred,
    int? total,
    String? errorMessage,
    int? expectedEpoch,
  }) {
    if (expectedEpoch != null && !_isContextCurrent(expectedEpoch)) {
      return;
    }
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(
        state.transfers.map(
          (transfer) => transfer.id == id
              ? transfer.copyWith(
                  status: status,
                  transferred: transferred,
                  total: total,
                  errorMessage: errorMessage,
                )
              : transfer,
        ),
      ),
    );
  }
}

final class _VersionTarget {
  const _VersionTarget({
    required this.path,
    required this.parentPath,
    required this.openedCurrentHash,
  });

  final String path;
  final String parentPath;
  final String? openedCurrentHash;
}

final RegExp _lowercaseBlake3DigestPattern = RegExp(r'^[0-9a-f]{64}$');

final class _StagedUploadPayload {
  const _StagedUploadPayload({required this.bytes, required this.sha256});

  final int bytes;
  final String sha256;
}

final class _DigestCaptureSink implements Sink<Digest> {
  Digest? _digest;

  Digest get value {
    final digest = _digest;
    if (digest == null) {
      throw StateError('Digest has not been finalized');
    }
    return digest;
  }

  @override
  void add(Digest data) {
    if (_digest != null) {
      throw StateError('Digest was finalized more than once');
    }
    _digest = data;
  }

  @override
  void close() {}
}

transfer_core.TransferPhase _uploadFailurePhase(Object error) {
  if (_isTerminalAuthenticationFailure(error)) {
    return transfer_core.TransferPhase.awaitingAuth;
  }
  if (error is ApiException) {
    final status = error.statusCode;
    if (error.kind == ApiFailureKind.cancelled ||
        error.kind == ApiFailureKind.connection ||
        error.kind == ApiFailureKind.timeout ||
        status == 408 ||
        status == 429 ||
        (status != null && status >= 500)) {
      return transfer_core.TransferPhase.paused;
    }
  }
  return transfer_core.TransferPhase.failed;
}

String _uploadFailureCode(Object error, transfer_core.TransferPhase phase) {
  if (phase == transfer_core.TransferPhase.awaitingAuth) {
    return 'AUTH_REQUIRED';
  }
  if (phase == transfer_core.TransferPhase.paused) {
    return error is ApiException && error.kind == ApiFailureKind.cancelled
        ? 'PAUSED_BY_USER'
        : 'RETRYABLE_UPLOAD_FAILURE';
  }
  return 'UPLOAD_FAILED';
}

bool _isUploadSessionGone(Object error) {
  if (error is! ApiException) {
    return false;
  }
  return error.statusCode == 404 ||
      error.statusCode == 410 ||
      error.code == 'UPLOAD_SESSION_NOT_FOUND' ||
      error.code == 'UPLOAD_SESSION_EXPIRED';
}

bool _isStructuredUploadSessionGone(Object error) {
  if (error is! ApiException ||
      error.kind != ApiFailureKind.response ||
      !error.hasStructuredServerError) {
    return false;
  }
  return (error.statusCode == 404 &&
          error.code == 'UPLOAD_SESSION_NOT_FOUND') ||
      (error.statusCode == 410 && error.code == 'UPLOAD_SESSION_EXPIRED');
}

bool _isStructuredUploadSessionExpired(Object error) {
  return error is ApiException &&
      error.kind == ApiFailureKind.response &&
      error.hasStructuredServerError &&
      error.statusCode == 410 &&
      error.code == 'UPLOAD_SESSION_EXPIRED';
}

transfer_core.TransferPhase _downloadFailurePhase(Object error) {
  if (_isTerminalAuthenticationFailure(error)) {
    return transfer_core.TransferPhase.awaitingAuth;
  }
  if (error is ApiException) {
    final status = error.statusCode;
    if (error.kind == ApiFailureKind.cancelled ||
        error.kind == ApiFailureKind.connection ||
        error.kind == ApiFailureKind.timeout ||
        status == 408 ||
        status == 429 ||
        (status != null && status >= 500)) {
      return transfer_core.TransferPhase.paused;
    }
  }
  return transfer_core.TransferPhase.failed;
}

String _downloadFailureCode(Object error, transfer_core.TransferPhase phase) {
  if (phase == transfer_core.TransferPhase.awaitingAuth) {
    return 'AUTH_REQUIRED';
  }
  if (phase == transfer_core.TransferPhase.paused) {
    return error is ApiException && error.kind == ApiFailureKind.cancelled
        ? 'PAUSED_BY_USER'
        : 'RETRYABLE_DOWNLOAD_FAILURE';
  }
  return 'DOWNLOAD_FAILED';
}

bool _isTransferPayloadPath(String path, String payloadDirectory) {
  final separator = Platform.pathSeparator;
  String normalize(String value) {
    var normalized = value.replaceAll(RegExp(r'[\\/]+'), separator);
    while (normalized.endsWith(separator) && normalized.length > 1) {
      normalized = normalized.substring(0, normalized.length - 1);
    }
    return Platform.isWindows ? normalized.toLowerCase() : normalized;
  }

  final root = normalize(payloadDirectory);
  final candidate = normalize(path);
  return candidate.startsWith('$root$separator') &&
      !candidate.substring(root.length + 1).contains(separator);
}

bool _canRetryDurableTransfer(transfer_core.TransferTask task) {
  return task.phase == transfer_core.TransferPhase.paused ||
      task.phase == transfer_core.TransferPhase.awaitingAuth ||
      task.phase == transfer_core.TransferPhase.awaitingDestination;
}

DateTime _nextTransferTimestamp(transfer_core.TransferTask task) {
  final now = DateTime.now().toUtc();
  return now.isAfter(task.updatedAt) ? now : task.updatedAt;
}

bool _isTerminalAuthenticationFailure(Object error) {
  if (error is! ApiException) {
    return false;
  }
  return const <String>{
    'AUTH_SESSION_MISSING',
    'AUTH_CONTEXT_CHANGED',
    'AUTH_SESSION_STORAGE_FAILED',
    'AUTH_SCOPE_CHANGED',
    'INVALID_AUTH_HEADER',
    'INVALID_TOKEN',
    'MISSING_AUTH_HEADER',
    'NOT_AUTHENTICATED',
    'TOKEN_EXPIRED',
    'TOKEN_REVOKED',
    'USER_DISABLED',
    'USER_NOT_FOUND',
  }.contains(error.code);
}

bool _isCancellation(Object error) =>
    error is ApiException && error.kind == ApiFailureKind.cancelled;

bool _isUnconfirmedVersionRestoreError(Object error) {
  if (error is! ApiException) {
    return true;
  }
  return switch (error.kind) {
    ApiFailureKind.connection ||
    ApiFailureKind.timeout ||
    ApiFailureKind.cancelled ||
    ApiFailureKind.invalidResponse => true,
    ApiFailureKind.response =>
      !error.hasStructuredServerError ||
          (error.statusCode != null && error.statusCode! >= 500),
    ApiFailureKind.local => false,
  };
}

ApiException _operationSuperseded() => const ApiException(
  kind: ApiFailureKind.cancelled,
  code: 'OPERATION_SUPERSEDED',
  message: 'The client context changed',
);

ApiException _versionTargetChanged() => const ApiException(
  kind: ApiFailureKind.local,
  code: 'VERSION_TARGET_CHANGED',
  message: 'The current file changed after version history was opened',
);

ApiException _versionRestoreResultUnconfirmed(Object cause) => ApiException(
  kind: ApiFailureKind.invalidResponse,
  code: versionRestoreResultUnconfirmedCode,
  message:
      'The version restore result could not be confirmed; refresh before retrying',
  cause: cause,
);

ApiException _versionRestoreConfirmedRefreshRequired(
  Object cause,
) => ApiException(
  kind: ApiFailureKind.local,
  code: versionRestoreConfirmedRefreshRequiredCode,
  message:
      'The version restore was confirmed, but refreshed history is unavailable',
  cause: cause,
);

String _trashDeletionNotice(
  TrashEmptyResult result, {
  required bool hasWarnings,
}) {
  final base = result.remaining.isNotEmpty
      ? '已永久删除 ${result.deleted.length} 项，'
            '${result.remaining.length} 项仍待处理。'
      : result.skipped.isNotEmpty
      ? result.deleted.isEmpty
            ? '所选 ${result.skipped.length} 项已不存在，回收站已更新。'
            : '已永久删除 ${result.deleted.length} 项，'
                  '${result.skipped.length} 项已不存在。'
      : '已永久删除 ${result.deleted.length} 项。';
  return hasWarnings ? '$base 服务器同时报告了清理或持久化警告。' : base;
}

String clientErrorMessage(Object error) {
  if (error is FormatException) {
    return error.message;
  }
  if (error is ApiException) {
    return switch (error.code) {
      'INVALID_CREDENTIALS' || 'INVALID_PASSWORD' => '用户名或密码不正确。',
      'LOGIN_RATE_LIMITED' => '登录尝试过于频繁，请稍后再试。',
      'PASSWORD_CHANGE_REQUIRED' => '需要先修改密码。',
      'AUTH_SESSION_MISSING' ||
      'AUTH_CONTEXT_CHANGED' ||
      'AUTH_SESSION_STORAGE_FAILED' ||
      'AUTH_SCOPE_CHANGED' ||
      'INVALID_AUTH_HEADER' ||
      'INVALID_TOKEN' ||
      'MISSING_AUTH_HEADER' ||
      'NOT_AUTHENTICATED' ||
      'TOKEN_EXPIRED' ||
      'TOKEN_REVOKED' ||
      'USER_DISABLED' ||
      'USER_NOT_FOUND' => '登录状态已失效，请重新登录。',
      'WRITE_BUSY' => '设备正在处理其他写入，请稍后重试。',
      'QUOTA_EXCEEDED' => '可用配额不足，无法完成操作。',
      'INSUFFICIENT_STORAGE' => '设备可用空间不足。',
      'UPLOAD_SOURCE_TOO_LARGE' => '单个上传文件不能超过 10 GiB。',
      'UPLOAD_STAGING_CAPACITY_EXCEEDED' => '设备可用空间不足，无法暂存上传内容。',
      'UPLOAD_STAGING_LIMIT' => '服务器上传暂存空间繁忙，请稍后重试。',
      'APP_BACKGROUNDED' => _appBackgroundedTransferPause.message,
      'CONFLICT' => '文件已发生变化，请刷新后重试。',
      'DELETE_TARGET_CHANGED' ||
      'DELETE_POLICY_CHANGED' => '文件或删除策略已变化，请刷新后重新确认。',
      'AUTH_REQUIRED' => '当前服务器未启用用户认证。',
      'READ_ONLY_ACCOUNT' => '当前账户为只读账户，不能修改回收站。',
      'TRASH_RECONCILIATION_REQUIRED' => '需要先刷新回收站并核对上一次操作结果。',
      'TRASH_MUTATION_IN_PROGRESS' => '另一项回收站操作仍在进行中。',
      'FILE_MUTATION_IN_PROGRESS' => '另一项文件操作仍在进行中，请等待其完成。',
      'DIRECTORY_REFRESH_REQUIRED' => '当前目录内容需要刷新，刷新成功后才能继续修改。',
      'FILE_RECONCILIATION_REQUIRED' => '上一项文件操作结果仍待核对。请刷新提示的相关目录后再继续修改。',
      'ADMIN_REQUIRED' => '只有管理员可以恢复文件版本。',
      'VERSION_TARGET_INVALID' => '只能查看可读取文件的版本历史。',
      'VERSION_IDENTITY_UNAVAILABLE' => '无法确认当前文件身份，请刷新目录后重试。',
      'VERSION_TARGET_MISMATCH' => '所选版本不属于当前文件，请重新打开版本历史。',
      'VERSION_TARGET_CHANGED' => '当前文件已发生变化，请刷新目录后重新打开版本历史。',
      'VERSION_ALREADY_CURRENT' => '所选版本已经是当前版本。',
      'VERSION_NOT_AVAILABLE' => '所选历史版本已不存在，请刷新版本历史。',
      versionRestoreResultUnconfirmedCode => '版本恢复结果无法确认。请先刷新目录和版本历史核对，避免重复提交。',
      versionRestoreConfirmedRefreshRequiredCode =>
        '版本已确认恢复，但版本历史刷新失败。请关闭后重新打开版本历史核对；不要重复恢复。',
      _ when error.kind == ApiFailureKind.cancelled => '传输已取消。',
      _ when error.kind == ApiFailureKind.timeout => '连接超时，请确认网络和设备状态。',
      _ when error.kind == ApiFailureKind.connection => '无法连接到设备，请确认地址和网络。',
      _ => error.message,
    };
  }
  if (error is FileSystemException) {
    return '无法访问本地文件，请检查存储权限和可用空间。';
  }
  if (error is PlatformException) {
    return switch (error.code) {
      'IMPORT_CANCELLED' ||
      'IMPORT_METADATA_CANCELLED' ||
      'EXPORT_CANCELLED' => '操作已取消。',
      'IMPORT_QUEUE_FULL' || 'EXPORT_QUEUE_FULL' => '正在处理的文件较多，请稍后重试。',
      'IMPORT_TOO_LARGE' => '单个上传文件不能超过 10 GiB。',
      'IMPORT_COPY_FAILED' ||
      'IMPORT_METADATA_FAILED' => '无法读取所选文件，请检查文件权限和可用空间。',
      'EXPORT_FAILED' => '无法写入所选位置，请检查目标权限和可用空间。',
      'FILE_PICKER_UNAVAILABLE' ||
      'FILE_PICKER_FAILED' ||
      'SAVE_DIALOG_UNAVAILABLE' ||
      'SAVE_DIALOG_FAILED' => '无法使用系统文件选择器，请重试或更换存储位置。',
      'TOO_MANY_IMPORTS' => '一次最多选择 100 个文件。',
      _ => '本地文件操作未完成，请重试。',
    };
  }
  return '操作未完成，请稍后重试。';
}

String _fileMutationFailureMessage(Object error, String action) {
  if (error is ApiException &&
      (error.isUnconfirmedMutation ||
          (error.statusCode != null && error.statusCode! >= 500))) {
    return '$action结果无法确认。请先刷新目录核对，避免重复提交。';
  }
  return clientErrorMessage(error);
}

String _joinLogicalPath(String parent, String name) {
  final prefix = parent == '/' ? '' : normalizeLogicalPath(parent);
  return normalizeLogicalPath('$prefix/$name', allowRoot: false);
}

String _logicalParentPath(String path) {
  final normalized = normalizeLogicalPath(path, allowRoot: false);
  final separator = normalized.lastIndexOf('/');
  return separator <= 0 ? '/' : normalized.substring(0, separator);
}
