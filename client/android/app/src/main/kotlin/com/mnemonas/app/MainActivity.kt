package com.mnemonas.app

import android.app.Activity
import android.content.Intent
import android.net.Uri
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.io.FileInputStream

class MainActivity : FlutterActivity() {
    companion object {
        private const val CHANNEL = "com.mnemonas.app/file_export"
        private const val CREATE_DOCUMENT_REQUEST = 4107
    }

    private var pendingCreateResult: MethodChannel.Result? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL)
            .setMethodCallHandler(::handleFileExportCall)
    }

    private fun handleFileExportCall(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "createDocument" -> createDocument(call, result)
            "copyToDocument" -> copyToDocument(call, result)
            else -> result.notImplemented()
        }
    }

    private fun createDocument(call: MethodCall, result: MethodChannel.Result) {
        if (pendingCreateResult != null) {
            result.error("EXPORT_IN_PROGRESS", "A save dialog is already open.", null)
            return
        }
        val suggestedName = sanitizeFileName(call.argument<String>("suggestedName"))
        val mimeType = sanitizeMimeType(call.argument<String>("mimeType"))
        val intent = Intent(Intent.ACTION_CREATE_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = mimeType
            putExtra(Intent.EXTRA_TITLE, suggestedName)
        }
        pendingCreateResult = result
        try {
            startActivityForResult(intent, CREATE_DOCUMENT_REQUEST)
        } catch (error: Exception) {
            pendingCreateResult = null
            result.error("SAVE_DIALOG_UNAVAILABLE", error.message, null)
        }
    }

    private fun copyToDocument(call: MethodCall, result: MethodChannel.Result) {
        val sourcePath = call.argument<String>("sourcePath")
        val targetValue = call.argument<String>("targetUri")
        if (sourcePath.isNullOrBlank() || targetValue.isNullOrBlank()) {
            result.error("INVALID_EXPORT", "Source path and target URI are required.", null)
            return
        }

        val source = try {
            File(sourcePath).canonicalFile
        } catch (error: Exception) {
            result.error("INVALID_EXPORT_SOURCE", error.message, null)
            return
        }
        if (!source.isFile || !isAppPrivateFile(source)) {
            result.error(
                "INVALID_EXPORT_SOURCE",
                "Only app-private regular files can be exported.",
                null,
            )
            return
        }

        val target = Uri.parse(targetValue)
        if (target.scheme != "content") {
            result.error("INVALID_EXPORT_TARGET", "A content URI is required.", null)
            return
        }

        Thread {
            try {
                FileInputStream(source).use { input ->
                    val output = contentResolver.openOutputStream(target, "w")
                        ?: throw IllegalStateException("The selected destination is unavailable.")
                    output.use { stream ->
                        input.copyTo(stream)
                        stream.flush()
                    }
                }
                runOnUiThread { result.success(null) }
            } catch (error: Exception) {
                runOnUiThread {
                    result.error("EXPORT_FAILED", error.message, null)
                }
            }
        }.start()
    }

    private fun isAppPrivateFile(file: File): Boolean {
        val allowedRoots = listOf(cacheDir.canonicalFile, filesDir.canonicalFile)
        return allowedRoots.any { root ->
            file.path.startsWith(root.path + File.separator)
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode != CREATE_DOCUMENT_REQUEST) {
            super.onActivityResult(requestCode, resultCode, data)
            return
        }
        val result = pendingCreateResult
        pendingCreateResult = null
        if (result == null) {
            return
        }
        if (resultCode == Activity.RESULT_CANCELED) {
            result.success(null)
            return
        }
        val uri = data?.data
        if (resultCode != Activity.RESULT_OK || uri?.scheme != "content") {
            result.error("SAVE_DIALOG_FAILED", "No writable destination was returned.", null)
            return
        }
        result.success(uri.toString())
    }

    override fun onDestroy() {
        pendingCreateResult?.error(
            "SAVE_DIALOG_CLOSED",
            "The save dialog closed before returning a destination.",
            null,
        )
        pendingCreateResult = null
        super.onDestroy()
    }

    private fun sanitizeFileName(value: String?): String {
        val candidate = value
            ?.replace(Regex("[\\u0000-\\u001F\\u007F/\\\\]"), "_")
            ?.trim()
            ?.take(180)
            .orEmpty()
        return candidate.ifBlank { "MnemoNAS-download" }
    }

    private fun sanitizeMimeType(value: String?): String {
        val candidate = value?.trim().orEmpty()
        return if (Regex("^[A-Za-z0-9!#$&^_.+-]+/[A-Za-z0-9!#$&^_.+-]+$")
                .matches(candidate)
        ) {
            candidate
        } else {
            "application/octet-stream"
        }
    }
}
