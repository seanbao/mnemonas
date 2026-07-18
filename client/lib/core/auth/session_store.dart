import 'dart:async';
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'auth_models.dart';

abstract interface class AuthSessionStore {
  Future<AuthSession?> load();

  Future<void> save(AuthSession session);

  Future<void> clear();
}

final class SecureAuthSessionStore implements AuthSessionStore {
  SecureAuthSessionStore({FlutterSecureStorage? storage})
    : _storage =
          storage ??
          const FlutterSecureStorage(
            aOptions: AndroidOptions(storageNamespace: 'mnemonas_client'),
          );

  static const _sessionKey = 'mnemonas.auth.session.v1';

  final FlutterSecureStorage _storage;
  Future<void> _operationTail = Future<void>.value();

  @override
  Future<AuthSession?> load() => _serialized(() async {
    final encoded = await _storage.read(key: _sessionKey);
    if (encoded == null || encoded.isEmpty) {
      return null;
    }
    try {
      final json = jsonDecode(encoded);
      if (json is! Map<String, dynamic>) {
        throw const FormatException('Invalid session record');
      }
      return AuthSession.fromJson(json);
    } on FormatException {
      await _storage.delete(key: _sessionKey);
      return null;
    } on JsonUnsupportedObjectError {
      await _storage.delete(key: _sessionKey);
      return null;
    }
  });

  @override
  Future<void> save(AuthSession session) => _serialized(() async {
    // The complete rotating pair is stored as one encrypted value so readers
    // can never observe an access token from one generation and a refresh
    // token from another.
    final encoded = jsonEncode(session.toJson());
    await _storage.write(key: _sessionKey, value: encoded);
  });

  @override
  Future<void> clear() => _serialized(() => _storage.delete(key: _sessionKey));

  Future<T> _serialized<T>(Future<T> Function() operation) {
    final result = Completer<T>();
    _operationTail = _operationTail.then((_) async {
      try {
        result.complete(await operation());
      } catch (error, stackTrace) {
        result.completeError(error, stackTrace);
      }
    });
    _operationTail = _operationTail.catchError((Object _) {});
    return result.future;
  }
}

final class MemoryAuthSessionStore implements AuthSessionStore {
  AuthSession? _session;

  @override
  Future<void> clear() async {
    _session = null;
  }

  @override
  Future<AuthSession?> load() async => _session;

  @override
  Future<void> save(AuthSession session) async {
    _session = session;
  }
}
