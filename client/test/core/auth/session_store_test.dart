import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_models.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mocktail/mocktail.dart';

void main() {
  test('stale revisions cannot replace or clear a newer session', () async {
    final store = MemoryAuthSessionStore();
    final empty = await store.snapshot();
    final first = _session('first');

    expect(await store.commitIfRevision(empty.revision, first), isTrue);
    expect(
      await store.commitIfRevision(empty.revision, _session('stale')),
      isFalse,
    );
    expect(await store.takeAndClearIfRevision(empty.revision), isNull);

    final current = await store.snapshot();
    expect(current.revision, empty.revision + 1);
    expect(current.session?.tokens.accessToken, 'access-first');
    expect(current.session?.tokens.refreshToken, 'refresh-first');
  });

  test('only one concurrent commit can win the same revision', () async {
    final store = MemoryAuthSessionStore();
    final empty = await store.snapshot();

    final results = await Future.wait([
      store.commitIfRevision(empty.revision, _session('one')),
      store.commitIfRevision(empty.revision, _session('two')),
    ]);

    expect(results.where((result) => result), hasLength(1));
    final current = await store.snapshot();
    expect(current.revision, empty.revision + 1);
    expect(
      current.session?.tokens.accessToken,
      anyOf('access-one', 'access-two'),
    );
  });

  test(
    'take and clear returns the previous session and advances revision',
    () async {
      final store = MemoryAuthSessionStore();
      final empty = await store.snapshot();
      expect(
        await store.commitIfRevision(empty.revision, _session('active')),
        isTrue,
      );
      final active = await store.snapshot();

      final cleared = await store.takeAndClearIfRevision(active.revision);

      expect(cleared, isNotNull);
      expect(cleared!.revision, active.revision + 1);
      expect(cleared.session?.tokens.accessToken, 'access-active');
      final current = await store.snapshot();
      expect(current.revision, cleared.revision);
      expect(current.session, isNull);
    },
  );

  test('clearing an empty store still invalidates stale operations', () async {
    final store = MemoryAuthSessionStore();
    final first = await store.snapshot();

    final cleared = await store.takeAndClear();

    expect(cleared.revision, first.revision + 1);
    expect(cleared.session, isNull);
    expect(
      await store.commitIfRevision(first.revision, _session('stale')),
      isFalse,
    );
    expect((await store.snapshot()).session, isNull);
  });

  test('secure storage persists one versioned CAS record', () async {
    final values = <String, String>{};
    final storage = _MockSecureStorage();
    when(() => storage.read(key: any(named: 'key'))).thenAnswer((
      invocation,
    ) async {
      return values[invocation.namedArguments[#key]! as String];
    });
    when(
      () => storage.write(
        key: any(named: 'key'),
        value: any(named: 'value'),
      ),
    ).thenAnswer((invocation) async {
      values[invocation.namedArguments[#key]! as String] =
          invocation.namedArguments[#value]! as String;
    });
    final store = SecureAuthSessionStore(storage: storage);

    final initial = await store.snapshot();
    expect(initial.revision, 0);
    expect(
      await store.commitIfRevision(initial.revision, _session('secure')),
      isTrue,
    );

    final encoded = values.values.single;
    final record = jsonDecode(encoded) as Map<String, dynamic>;
    expect(record['schema_version'], 2);
    expect(record['revision'], 1);
    expect(record['session'], isA<Map<String, dynamic>>());

    final restored = await SecureAuthSessionStore(storage: storage).snapshot();
    expect(restored.revision, 1);
    expect(restored.session?.tokens.accessToken, 'access-secure');
  });

  test('invalid secure records become revisioned empty tombstones', () async {
    final values = <String, String>{'mnemonas.auth.session.v2': '{invalid'};
    final storage = _MockSecureStorage();
    when(() => storage.read(key: any(named: 'key'))).thenAnswer((
      invocation,
    ) async {
      return values[invocation.namedArguments[#key]! as String];
    });
    when(
      () => storage.write(
        key: any(named: 'key'),
        value: any(named: 'value'),
      ),
    ).thenAnswer((invocation) async {
      values[invocation.namedArguments[#key]! as String] =
          invocation.namedArguments[#value]! as String;
    });
    final store = SecureAuthSessionStore(storage: storage);

    final snapshot = await store.snapshot();

    expect(snapshot.revision, 1);
    expect(snapshot.session, isNull);
    expect(await store.commitIfRevision(0, _session('stale')), isFalse);
    final record =
        jsonDecode(values['mnemonas.auth.session.v2']!) as Map<String, dynamic>;
    expect(record, {'schema_version': 2, 'revision': 1, 'session': null});
  });
}

final class _MockSecureStorage extends Mock implements FlutterSecureStorage {}

AuthSession _session(String generation) {
  return AuthSession(
    serverBaseUrl: 'https://nas.example.com',
    tokens: AuthTokenPair(
      accessToken: 'access-$generation',
      refreshToken: 'refresh-$generation',
      expiresAt: DateTime.utc(2026, 7, 19, 13),
    ),
  );
}
