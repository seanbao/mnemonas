import 'dart:async';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/app/client_app.dart';

void main() {
  test(
    'transient cache cleanup preserves files created after its frozen start',
    () async {
      final root = await Directory.systemTemp.createTemp(
        'mnemonas-client-cache-cleanup-',
      );
      addTearDown(() async {
        if (await root.exists()) {
          await root.delete(recursive: true);
        }
      });
      final downloads = Directory('${root.path}/mnemonas/version-downloads');
      await downloads.create(recursive: true);

      final cleanupStartedAt = DateTime.now().toUtc();
      final orphan = File('${downloads.path}/orphan.bin');
      await orphan.writeAsBytes(<int>[1]);
      await orphan.setLastModified(
        cleanupStartedAt.subtract(const Duration(minutes: 10)),
      );

      final providerCalled = Completer<void>();
      final provideRoot = Completer<Directory>();
      final cleanup = cleanupTransientFileCacheForTesting(
        temporaryDirectoryProvider: () {
          providerCalled.complete();
          return provideRoot.future;
        },
        cleanupStartedAt: cleanupStartedAt,
      );
      await providerCalled.future;

      final concurrent = File('${downloads.path}/active.bin');
      await concurrent.writeAsBytes(<int>[2]);
      await concurrent.setLastModified(
        cleanupStartedAt.add(const Duration(seconds: 1)),
      );
      provideRoot.complete(root);
      await cleanup;

      expect(await orphan.exists(), isFalse);
      expect(await concurrent.exists(), isTrue);

      await cleanupTransientFileCacheForTesting(
        temporaryDirectoryProvider: () async => root,
        cleanupStartedAt: cleanupStartedAt.add(const Duration(minutes: 3)),
      );
      expect(await concurrent.exists(), isFalse);
    },
  );

  test(
    'transient cache cleanup absorbs temporary-directory failures',
    () async {
      await expectLater(
        cleanupTransientFileCacheForTesting(
          temporaryDirectoryProvider: () => Future<Directory>.error(
            PlatformException(code: 'path_provider_unavailable'),
          ),
          cleanupStartedAt: DateTime.now().toUtc(),
        ),
        completes,
      );
    },
  );
}
