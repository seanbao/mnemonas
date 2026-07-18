import 'dart:async';
import 'dart:io';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/auth/auth_api.dart';
import '../core/auth/session_store.dart';
import '../core/files/file_models.dart';
import '../core/files/file_path.dart';
import '../core/files/files_api.dart';
import '../core/network/api_client.dart';
import '../core/network/api_error.dart';
import '../core/server/server_endpoint.dart';
import '../core/server/server_preferences.dart';
import '../core/system/system_api.dart';
import '../core/system/system_models.dart';
import '../core/trash/trash_api.dart';
import '../core/trash/trash_models.dart';
import 'client_state.dart';

final serverPreferencesProvider = Provider<ServerPreferencesStore>(
  (ref) => ServerPreferences(),
);

final authSessionStoreProvider = Provider<AuthSessionStore>(
  (ref) => SecureAuthSessionStore(),
);

typedef ApiClientFactory =
    ApiClient Function(ServerEndpoint endpoint, AuthSessionStore sessionStore);

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

  ApiClient? _client;
  AuthApi? _auth;
  FilesApi? _files;
  SystemApi? _system;
  TrashApi? _trash;
  CancelToken? _directoryCancellation;
  CancelToken? _overviewCancellation;
  CancelToken? _trashReadCancellation;
  CancelToken? _trashMutationCancellation;
  Future<void> _preferenceMutationTail = Future<void>.value();
  var _contextEpoch = 0;
  var _directorySequence = 0;
  var _trashSequence = 0;
  var _transferSequence = 0;
  var _disposed = false;
  final Map<String, CancelToken> _transferCancellations =
      <String, CancelToken>{};

  DirectoryListing? get currentDirectory => state.directory;

  @override
  ClientState build() {
    _serverPreferences = ref.watch(serverPreferencesProvider);
    _sessionStore = ref.watch(authSessionStoreProvider);
    _apiClientFactory = ref.watch(apiClientFactoryProvider);
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
    _directoryCancellation?.cancel('Superseded directory request');
    final cancellation = CancelToken();
    _directoryCancellation = cancellation;
    final files = _requireFiles();
    state = state.copyWith(
      currentPath: normalized,
      directory: null,
      isBusy: true,
      errorMessage: null,
    );
    try {
      final response = await files.list(normalized, cancelToken: cancellation);
      if (!_isDirectoryRequestCurrent(epoch, sequence, cancellation)) {
        return;
      }
      state = state.copyWith(
        currentPath: response.data.path,
        directory: response.data,
        isBusy: false,
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
        state = state.copyWith(
          isBusy: false,
          errorMessage: clientErrorMessage(error),
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

  Future<void> createDirectory(String name) async {
    final trimmed = name.trim();
    if (trimmed.isEmpty || trimmed.contains('/')) {
      throw const FormatException('文件夹名称不能为空或包含斜杠');
    }
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    final target = _joinLogicalPath(refreshPath, trimmed);
    state = state.copyWith(isBusy: true, errorMessage: null);
    try {
      final result = await files.createDirectory(target);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      await loadDirectory(refreshPath);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (result.hasWarnings || result.data.persistenceWarning) {
        state = state.copyWith(notice: '文件夹已创建，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
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

  Future<void> renameEntry(FileEntry entry, String newName) async {
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    state = state.copyWith(isBusy: true, errorMessage: null);
    try {
      final result = await files.rename(
        logicalPath: entry.path,
        newName: newName,
      );
      if (!_isContextCurrent(epoch)) {
        return;
      }
      await loadDirectory(refreshPath);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (result.hasWarnings || result.data.persistenceWarning) {
        state = state.copyWith(notice: '项目已重命名，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
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
    state = state.copyWith(isBusy: true, errorMessage: null);
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
      await loadDirectory(refreshPath);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (hasWarnings) {
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
      try {
        await loadDirectory(refreshPath);
      } on Object catch (refreshError) {
        if (!_isContextCurrent(epoch)) {
          return;
        }
        if (!await handleAuthenticationFailure(
          refreshError,
          expectedEpoch: epoch,
        )) {
          state = state.copyWith(isBusy: false);
        }
      }
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (state.stage == ClientStage.needsLogin) {
        rethrow;
      }
      state = state.copyWith(
        errorMessage: completed == 0
            ? clientErrorMessage(error)
            : '已完成 $completed 项，后续操作失败。请刷新目录确认结果。',
      );
      rethrow;
    }
  }

  Future<DeleteIntentSnapshot> prepareDelete(List<FileEntry> entries) async {
    final epoch = _contextEpoch;
    final files = _requireFiles();
    state = state.copyWith(isBusy: true, errorMessage: null);
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
    }
  }

  Future<void> deleteConfirmed(DeleteIntentSnapshot snapshot) async {
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    state = state.copyWith(isBusy: true, errorMessage: null);
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
      await loadDirectory(refreshPath);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (hasWarnings) {
        state = state.copyWith(notice: '删除已完成，但服务器报告了清理或持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch) || _isCancellation(error)) {
        return;
      }
      if (await handleAuthenticationFailure(error, expectedEpoch: epoch)) {
        rethrow;
      }
      try {
        await loadDirectory(refreshPath);
      } on Object catch (refreshError) {
        if (!_isContextCurrent(epoch)) {
          return;
        }
        if (!await handleAuthenticationFailure(
          refreshError,
          expectedEpoch: epoch,
        )) {
          state = state.copyWith(isBusy: false);
        }
      }
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (state.stage == ClientStage.needsLogin) {
        rethrow;
      }
      state = state.copyWith(
        errorMessage: completed == 0
            ? clientErrorMessage(error)
            : '已删除 $completed 项，后续操作失败。请刷新目录确认结果。',
      );
      rethrow;
    }
  }

  Future<void> uploadFile({
    required String sourcePath,
    required String fileName,
  }) async {
    final epoch = _contextEpoch;
    final refreshPath = state.currentPath;
    final files = _requireFiles();
    final target = _joinLogicalPath(refreshPath, fileName);
    final id = _nextTransferId();
    final cancelToken = CancelToken();
    _transferCancellations[id] = cancelToken;
    _addTransfer(
      ClientTransfer(
        id: id,
        name: fileName,
        direction: TransferDirection.upload,
        status: TransferStatus.running,
        transferred: 0,
        total: -1,
      ),
      expectedEpoch: epoch,
    );
    try {
      final result = await files.uploadFile(
        logicalPath: target,
        sourcePath: sourcePath,
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
        return;
      }
      _updateTransfer(
        id,
        status: TransferStatus.completed,
        expectedEpoch: epoch,
      );
      await loadDirectory(refreshPath);
      if (!_isContextCurrent(epoch)) {
        return;
      }
      if (result.hasWarnings || result.data.persistenceWarning) {
        state = state.copyWith(notice: '文件已上传，但服务器报告了持久化警告。');
      }
    } on Object catch (error) {
      if (!_isContextCurrent(epoch)) {
        return;
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
      _transferCancellations.remove(id);
    }
  }

  Future<File> downloadFile({
    required FileEntry entry,
    required String destinationPath,
    bool overwrite = false,
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
      _transferCancellations.remove(id);
    }
  }

  void cancelTransfer(String id) {
    final token = _transferCancellations[id];
    if (token == null || token.isCancelled) {
      return;
    }
    token.cancel('Cancelled by the user');
    _updateTransfer(id, status: TransferStatus.cancelled);
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
    _system = SystemApi(client);
    _trash = TrashApi(client);
    return epoch;
  }

  int _invalidateContext() {
    _contextEpoch++;
    _directorySequence++;
    _trashSequence++;
    _directoryCancellation?.cancel('Client context changed');
    _directoryCancellation = null;
    _overviewCancellation?.cancel('Client context changed');
    _overviewCancellation = null;
    _trashReadCancellation?.cancel('Client context changed');
    _trashReadCancellation = null;
    _trashMutationCancellation?.cancel('Client context changed');
    _trashMutationCancellation = null;
    _cancelAllTransfers();
    _client?.close();
    _client = null;
    _auth = null;
    _files = null;
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

  void _addTransfer(ClientTransfer transfer, {int? expectedEpoch}) {
    if (expectedEpoch != null && !_isContextCurrent(expectedEpoch)) {
      return;
    }
    state = state.copyWith(
      transfers: List<ClientTransfer>.unmodifiable(<ClientTransfer>[
        transfer,
        ...state.transfers.take(19),
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

ApiException _operationSuperseded() => const ApiException(
  kind: ApiFailureKind.cancelled,
  code: 'OPERATION_SUPERSEDED',
  message: 'The client context changed',
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
      'CONFLICT' => '文件已发生变化，请刷新后重试。',
      'DELETE_TARGET_CHANGED' ||
      'DELETE_POLICY_CHANGED' => '文件或删除策略已变化，请刷新后重新确认。',
      'AUTH_REQUIRED' => '当前服务器未启用用户认证。',
      'READ_ONLY_ACCOUNT' => '当前账户为只读账户，不能修改回收站。',
      'TRASH_RECONCILIATION_REQUIRED' => '需要先刷新回收站并核对上一次操作结果。',
      'TRASH_MUTATION_IN_PROGRESS' => '另一项回收站操作仍在进行中。',
      _ when error.kind == ApiFailureKind.cancelled => '传输已取消。',
      _ when error.kind == ApiFailureKind.timeout => '连接超时，请确认网络和设备状态。',
      _ when error.kind == ApiFailureKind.connection => '无法连接到设备，请确认地址和网络。',
      _ => error.message,
    };
  }
  if (error is FileSystemException) {
    return '无法访问本地文件，请检查存储权限和可用空间。';
  }
  return '操作未完成，请稍后重试。';
}

String _joinLogicalPath(String parent, String name) {
  final prefix = parent == '/' ? '' : normalizeLogicalPath(parent);
  return normalizeLogicalPath('$prefix/$name', allowRoot: false);
}
