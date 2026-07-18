import 'dart:async';
import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'auth_models.dart';

final class AuthSessionSnapshot {
  const AuthSessionSnapshot({required this.revision, required this.session});

  final int revision;
  final AuthSession? session;
}

final class AuthSessionStoreException implements Exception {
  const AuthSessionStoreException(this.operation, [this.cause]);

  final String operation;
  final Object? cause;

  @override
  String toString() => 'AuthSessionStoreException($operation)';
}

abstract interface class AuthSessionStore {
  Future<AuthSessionSnapshot> snapshot();

  Future<bool> commitIfRevision(int expectedRevision, AuthSession session);

  Future<AuthSessionSnapshot?> takeAndClearIfRevision(int expectedRevision);

  Future<AuthSessionSnapshot> takeAndClear();
}

final class SecureAuthSessionStore implements AuthSessionStore {
  SecureAuthSessionStore({FlutterSecureStorage? storage})
    : _storage =
          storage ??
          const FlutterSecureStorage(
            aOptions: AndroidOptions(storageNamespace: 'mnemonas_client'),
          );

  static const _sessionKey = 'mnemonas.auth.session.v2';
  static const _schemaVersion = 2;

  final FlutterSecureStorage _storage;
  Future<void> _operationTail = Future<void>.value();

  @override
  Future<AuthSessionSnapshot> snapshot() =>
      _serialized(() async => (await _readRecord()).snapshot);

  @override
  Future<bool> commitIfRevision(int expectedRevision, AuthSession session) =>
      _serialized(() async {
        final current = await _readRecord();
        if (current.revision != expectedRevision) {
          return false;
        }
        await _writeRecord(
          _StoredSessionRecord(
            revision: current.revision + 1,
            session: session,
          ),
        );
        return true;
      });

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(int expectedRevision) =>
      _serialized(() async {
        final current = await _readRecord();
        if (current.revision != expectedRevision) {
          return null;
        }
        final nextRevision = current.revision + 1;
        await _writeRecord(
          _StoredSessionRecord(revision: nextRevision, session: null),
        );
        return AuthSessionSnapshot(
          revision: nextRevision,
          session: current.session,
        );
      });

  @override
  Future<AuthSessionSnapshot> takeAndClear() => _serialized(() async {
    final current = await _readRecord();
    final nextRevision = current.revision + 1;
    await _writeRecord(
      _StoredSessionRecord(revision: nextRevision, session: null),
    );
    return AuthSessionSnapshot(
      revision: nextRevision,
      session: current.session,
    );
  });

  Future<_StoredSessionRecord> _readRecord() async {
    try {
      final encoded = await _storage.read(key: _sessionKey);
      if (encoded == null || encoded.isEmpty) {
        return const _StoredSessionRecord(revision: 0, session: null);
      }
      final json = jsonDecode(encoded);
      if (json is! Map<String, dynamic>) {
        throw const FormatException('Invalid session record');
      }
      return _StoredSessionRecord.fromJson(json);
    } on AuthSessionStoreException {
      rethrow;
    } on FormatException {
      return _replaceInvalidRecord();
    } on JsonUnsupportedObjectError {
      return _replaceInvalidRecord();
    } on Object catch (error) {
      throw AuthSessionStoreException('read', error);
    }
  }

  Future<_StoredSessionRecord> _replaceInvalidRecord() async {
    const replacement = _StoredSessionRecord(revision: 1, session: null);
    try {
      await _writeRecord(replacement);
      return replacement;
    } on Object catch (error) {
      throw AuthSessionStoreException('replace_invalid', error);
    }
  }

  Future<void> _writeRecord(_StoredSessionRecord record) async {
    try {
      // The rotating pair and its revision are committed as one encrypted
      // value so readers never observe tokens from different generations.
      await _storage.write(
        key: _sessionKey,
        value: jsonEncode(record.toJson()),
      );
    } on Object catch (error) {
      throw AuthSessionStoreException('write', error);
    }
  }

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
  int _revision = 0;
  AuthSession? _session;

  @override
  Future<AuthSessionSnapshot> snapshot() async {
    return AuthSessionSnapshot(revision: _revision, session: _session);
  }

  @override
  Future<bool> commitIfRevision(
    int expectedRevision,
    AuthSession session,
  ) async {
    if (_revision != expectedRevision) {
      return false;
    }
    _revision++;
    _session = session;
    return true;
  }

  @override
  Future<AuthSessionSnapshot?> takeAndClearIfRevision(
    int expectedRevision,
  ) async {
    if (_revision != expectedRevision) {
      return null;
    }
    final previous = _session;
    _revision++;
    _session = null;
    return AuthSessionSnapshot(revision: _revision, session: previous);
  }

  @override
  Future<AuthSessionSnapshot> takeAndClear() async {
    final previous = _session;
    _revision++;
    _session = null;
    return AuthSessionSnapshot(revision: _revision, session: previous);
  }
}

final class _StoredSessionRecord {
  const _StoredSessionRecord({required this.revision, required this.session});

  factory _StoredSessionRecord.fromJson(Map<String, dynamic> json) {
    if (json['schema_version'] != SecureAuthSessionStore._schemaVersion) {
      throw const FormatException('Unsupported session store schema');
    }
    final revision = json['revision'];
    if (revision is! int || revision < 0) {
      throw const FormatException('Invalid session store revision');
    }
    final sessionJson = json['session'];
    if (sessionJson != null && sessionJson is! Map) {
      throw const FormatException('Invalid stored session');
    }
    return _StoredSessionRecord(
      revision: revision,
      session: sessionJson == null
          ? null
          : AuthSession.fromJson(Map<String, dynamic>.from(sessionJson)),
    );
  }

  final int revision;
  final AuthSession? session;

  AuthSessionSnapshot get snapshot =>
      AuthSessionSnapshot(revision: revision, session: session);

  Map<String, dynamic> toJson() => {
    'schema_version': SecureAuthSessionStore._schemaVersion,
    'revision': revision,
    'session': session?.toJson(),
  };
}
