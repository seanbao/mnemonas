import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_api.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/search/search_api.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';
import 'package:mnemonas_client/core/trash/trash_api.dart';
import 'package:mnemonas_client/core/trash/trash_models.dart';

void main() {
  final serverUrl = Platform.environment['MNEMONAS_CLIENT_E2E_URL'];
  final username = Platform.environment['MNEMONAS_CLIENT_E2E_USERNAME'];
  final password = Platform.environment['MNEMONAS_CLIENT_E2E_PASSWORD'];
  final replacementPassword =
      Platform.environment['MNEMONAS_CLIENT_E2E_REPLACEMENT_PASSWORD'];
  final skipReason =
      serverUrl == null ||
          username == null ||
          password == null ||
          replacementPassword == null
      ? 'live MnemoNAS credentials were not supplied'
      : false;

  test(
    'native client APIs complete an authenticated file lifecycle',
    () async {
      final endpoint = ServerEndpoint.parse(serverUrl!);
      final liveUsername = username!;
      final initialPassword = password!;
      final nextPassword = replacementPassword!;
      final sessionStore = MemoryAuthSessionStore();
      final client = ApiClient(endpoint: endpoint, sessionStore: sessionStore);
      final auth = AuthApi(client);
      final files = FilesApi(client);
      final search = SearchApi(client);
      final trash = TrashApi(client);
      var activePassword = initialPassword;
      var loggedIn = false;
      final suffix = DateTime.now().microsecondsSinceEpoch;
      final directoryName = '.mnemonas-client-smoke-$suffix';
      final directoryPath = '/$directoryName';
      final uploadedName = 'payload-$suffix.txt';
      final uploadedPath = '$directoryPath/$uploadedName';
      final renamedName = 'renamed-$suffix.txt';
      final renamedPath = '$directoryPath/$renamedName';
      final copiedName = 'copied-$suffix.txt';
      final sourceDirectory = await Directory.systemTemp.createTemp(
        'mnemonas-client-live-',
      );
      addTearDown(() => sourceDirectory.delete(recursive: true));
      final source = File('${sourceDirectory.path}/payload.txt');
      final replacementSource = File('${sourceDirectory.path}/replacement.txt');
      const payload = 'MnemoNAS native client live transfer\n';
      const replacementPayload = 'MnemoNAS native client version replacement\n';
      await source.writeAsString(payload, flush: true);
      await replacementSource.writeAsString(replacementPayload, flush: true);

      Future<void> removeRemoteFixture() async {
        try {
          final root = await files.list('/');
          FileEntry? directory;
          for (final entry in root.data.entries) {
            if (entry.path == directoryPath) {
              directory = entry;
              break;
            }
          }
          if (directory != null) {
            final intent = await files.prepareDeleteIntent([directory]);
            await files.delete(intent.data.confirmationForPath(directoryPath));
          }
        } on Object {
          // Cleanup is best effort; the primary assertion reports the failure.
        }
        try {
          final listing = await trash.list();
          final ids = listing.data.items
              .where((item) => item.originalPath == directoryPath)
              .map((item) => item.id)
              .toList(growable: false);
          for (
            var start = 0;
            start < ids.length;
            start += maxTrashSelectionIds
          ) {
            final candidateEnd = start + maxTrashSelectionIds;
            final end = candidateEnd < ids.length ? candidateEnd : ids.length;
            await trash.emptySelection(
              TrashSelectionSnapshot.fromIds(ids.sublist(start, end)),
            );
          }
        } on Object {
          // Trash cleanup is also best effort during test teardown.
        }
      }

      addTearDown(() async {
        if (loggedIn) {
          await removeRemoteFixture();
          try {
            await auth.logout();
          } on Object {
            await auth.forgetSession();
          }
        }
      });

      var login = await auth.login(
        username: liveUsername,
        password: activePassword,
      );
      loggedIn = true;
      if (login.data.user.mustChangePassword) {
        await auth.changePassword(
          currentPassword: activePassword,
          newPassword: nextPassword,
          expectedUserId: login.data.user.id,
        );
        await auth.forgetSession();
        loggedIn = false;
        activePassword = nextPassword;
        login = await auth.login(
          username: liveUsername,
          password: activePassword,
        );
        loggedIn = true;
      }

      expect(login.data.user.mustChangePassword, isFalse);
      final beforeRefresh =
          (await sessionStore.snapshot()).session!.tokens.refreshToken;
      await auth.refresh();
      final afterRefresh =
          (await sessionStore.snapshot()).session!.tokens.refreshToken;
      expect(afterRefresh, isNot(beforeRefresh));
      expect((await auth.me()).data.username, liveUsername);

      await files.createDirectory(directoryPath);
      await files.uploadFile(
        logicalPath: uploadedPath,
        sourcePath: source.path,
      );
      var listing = await files.list(directoryPath);
      final uploaded = _entryAt(listing.data, uploadedName);
      expect(uploaded.size, utf8.encode(payload).length);
      final originalHistory = await files.listVersions(uploaded.path);
      final originalHash = originalHistory.data.current.hash;
      expect(
        (await search.search(
          uploadedName,
        )).data.results.map((result) => result.path).toList(growable: false),
        <String>[uploadedPath],
      );

      final downloadedPath = '${sourceDirectory.path}/downloaded.txt';
      final downloaded = await files.downloadFile(
        logicalPath: uploaded.path,
        destinationPath: downloadedPath,
      );
      expect(await downloaded.file.readAsString(), payload);

      await files.uploadFile(
        logicalPath: uploaded.path,
        sourcePath: replacementSource.path,
      );
      listing = await files.list(directoryPath);
      expect(
        _entryAt(listing.data, uploadedName).size,
        utf8.encode(replacementPayload).length,
      );
      final history = await files.listVersions(uploaded.path);
      expect(history.data.current.hash, isNot(originalHash));
      final historical = history.data.versions.singleWhere(
        (version) => !version.isCurrent && version.hash == originalHash,
      );
      final historicalDownload = await files.downloadVersion(
        logicalPath: uploaded.path,
        version: historical,
        destinationPath: '${sourceDirectory.path}/historical.txt',
      );
      expect(await historicalDownload.file.readAsString(), payload);
      if (login.data.user.role == 'admin') {
        final restored = await files.restoreVersion(
          logicalPath: uploaded.path,
          hash: historical.hash,
        );
        expect(restored.data.restoredHash, historical.hash);
        final restoredHistory = await files.listVersions(uploaded.path);
        expect(restoredHistory.data.current.hash, historical.hash);
        final restoredDownload = await files.downloadFile(
          logicalPath: uploaded.path,
          destinationPath: '${sourceDirectory.path}/restored.txt',
        );
        expect(await restoredDownload.file.readAsString(), payload);
      }

      await files.rename(logicalPath: uploaded.path, newName: renamedName);
      expect((await search.search(uploadedName)).data.results, isEmpty);
      expect(
        (await search.search(
          renamedName,
        )).data.results.map((result) => result.path).toList(growable: false),
        <String>[renamedPath],
      );
      await files.copy(
        sourcePath: renamedPath,
        destinationPath: '$directoryPath/$copiedName',
      );
      listing = await files.list(directoryPath);
      expect(
        listing.data.entries.map((entry) => entry.name),
        containsAll(<String>[renamedName, copiedName]),
      );

      final rootBeforeDelete = await files.list('/');
      final remoteDirectory = rootBeforeDelete.data.entries.singleWhere(
        (entry) => entry.path == directoryPath,
      );
      final deleteIntent = await files.prepareDeleteIntent([remoteDirectory]);
      expect(deleteIntent.data.policy.mode, DeleteMode.trash);
      await files.delete(deleteIntent.data.confirmationForPath(directoryPath));
      expect(
        (await files.list(
          '/',
        )).data.entries.any((entry) => entry.path == directoryPath),
        isFalse,
      );
      expect((await search.search(renamedName)).data.results, isEmpty);

      var trashListing = await trash.list();
      final trashed = trashListing.data.items.singleWhere(
        (item) => item.originalPath == directoryPath,
      );
      await trash.restore(id: trashed.id);
      expect(
        (await files.list(
          '/',
        )).data.entries.any((entry) => entry.path == directoryPath),
        isTrue,
      );
      expect(
        (await search.search(
          renamedName,
        )).data.results.map((result) => result.path).toList(growable: false),
        <String>[renamedPath],
      );

      final restoredDirectory = (await files.list(
        '/',
      )).data.entries.singleWhere((entry) => entry.path == directoryPath);
      final secondDeleteIntent = await files.prepareDeleteIntent([
        restoredDirectory,
      ]);
      expect(secondDeleteIntent.data.policy.mode, DeleteMode.trash);
      await files.delete(
        secondDeleteIntent.data.confirmationForPath(directoryPath),
      );
      trashListing = await trash.list();
      final permanentlyDeleted = trashListing.data.items.singleWhere(
        (item) => item.originalPath == directoryPath,
      );
      final purge = await trash.emptySelection(
        TrashSelectionSnapshot.fromIds(<String>[permanentlyDeleted.id]),
      );
      expect(purge.data.deleted, <String>[permanentlyDeleted.id]);
      expect(purge.data.remaining, isEmpty);
      expect(purge.data.skipped, isEmpty);
      expect(
        (await trash.list()).data.items.any(
          (item) => item.id == permanentlyDeleted.id,
        ),
        isFalse,
      );
      expect((await search.search(renamedName)).data.results, isEmpty);
      await auth.logout();
      loggedIn = false;
      expect((await sessionStore.snapshot()).session, isNull);
    },
    skip: skipReason,
  );
}

FileEntry _entryAt(DirectoryListing listing, String name) {
  return listing.entries.singleWhere((entry) => entry.name == name);
}
