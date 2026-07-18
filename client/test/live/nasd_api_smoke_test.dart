import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:mnemonas_client/core/auth/auth_api.dart';
import 'package:mnemonas_client/core/auth/session_store.dart';
import 'package:mnemonas_client/core/files/file_models.dart';
import 'package:mnemonas_client/core/files/files_api.dart';
import 'package:mnemonas_client/core/network/api_client.dart';
import 'package:mnemonas_client/core/server/server_endpoint.dart';

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
      var activePassword = initialPassword;
      var loggedIn = false;
      final suffix = DateTime.now().microsecondsSinceEpoch;
      final directoryName = '.mnemonas-client-smoke-$suffix';
      final directoryPath = '/$directoryName';
      final sourceDirectory = await Directory.systemTemp.createTemp(
        'mnemonas-client-live-',
      );
      addTearDown(() => sourceDirectory.delete(recursive: true));
      final source = File('${sourceDirectory.path}/payload.txt');
      const payload = 'MnemoNAS native client live transfer\n';
      await source.writeAsString(payload, flush: true);

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
          if (directory == null) {
            return;
          }
          final intent = await files.prepareDeleteIntent([directory]);
          await files.delete(intent.data.confirmationForPath(directoryPath));
        } on Object {
          // Cleanup is best effort; the primary assertion reports the failure.
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
        loggedIn = false;
        activePassword = nextPassword;
        login = await auth.login(
          username: liveUsername,
          password: activePassword,
        );
        loggedIn = true;
      }

      expect(login.data.user.mustChangePassword, isFalse);
      final beforeRefresh = (await sessionStore.load())!.tokens.refreshToken;
      await auth.refresh();
      final afterRefresh = (await sessionStore.load())!.tokens.refreshToken;
      expect(afterRefresh, isNot(beforeRefresh));
      expect((await auth.me()).data.username, liveUsername);

      await files.createDirectory(directoryPath);
      await files.uploadFile(
        logicalPath: '$directoryPath/payload.txt',
        sourcePath: source.path,
      );
      var listing = await files.list(directoryPath);
      final uploaded = _entryAt(listing.data, 'payload.txt');
      expect(uploaded.size, utf8.encode(payload).length);

      final downloadedPath = '${sourceDirectory.path}/downloaded.txt';
      final downloaded = await files.downloadFile(
        logicalPath: uploaded.path,
        destinationPath: downloadedPath,
      );
      expect(await downloaded.file.readAsString(), payload);

      await files.rename(logicalPath: uploaded.path, newName: 'renamed.txt');
      await files.copy(
        sourcePath: '$directoryPath/renamed.txt',
        destinationPath: '$directoryPath/copied.txt',
      );
      listing = await files.list(directoryPath);
      expect(
        listing.data.entries.map((entry) => entry.name),
        containsAll(<String>['renamed.txt', 'copied.txt']),
      );

      await removeRemoteFixture();
      expect(
        (await files.list(
          '/',
        )).data.entries.any((entry) => entry.path == directoryPath),
        isFalse,
      );
      await auth.logout();
      loggedIn = false;
      expect(await sessionStore.load(), isNull);
    },
    skip: skipReason,
  );
}

FileEntry _entryAt(DirectoryListing listing, String name) {
  return listing.entries.singleWhere((entry) => entry.name == name);
}
