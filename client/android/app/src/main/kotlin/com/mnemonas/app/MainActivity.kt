package com.mnemonas.app

import android.app.Activity
import android.content.ContentResolver
import android.content.Intent
import android.net.Uri
import android.os.CancellationSignal
import android.os.Handler
import android.os.Looper
import android.provider.DocumentsContract
import android.provider.OpenableColumns
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import java.io.Closeable
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.util.UUID
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Future
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.ThreadFactory
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger

class MainActivity : FlutterActivity() {
    companion object {
        private const val EXPORT_CHANNEL = "com.mnemonas.app/file_export"
        private const val IMPORT_CHANNEL = "com.mnemonas.app/file_import"
        private const val CREATE_DOCUMENT_REQUEST = 4107
        private const val OPEN_DOCUMENTS_REQUEST = 4108
        private const val COPY_BUFFER_SIZE = 64 * 1024
        private const val PROGRESS_INTERVAL_BYTES = 256 * 1024L
        private const val MAX_SELECTED_DOCUMENTS = 100
        private const val MAX_QUEUED_OPERATIONS = 16
        private val OPERATION_ID_PATTERN = Regex("^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")
    }

    private val mainHandler = Handler(Looper.getMainLooper())
    private val workerSequence = AtomicInteger()
    private val ioExecutor = ThreadPoolExecutor(
        2,
        2,
        0L,
        TimeUnit.MILLISECONDS,
        ArrayBlockingQueue(MAX_QUEUED_OPERATIONS),
        ThreadFactory { runnable ->
            Thread(
                runnable,
                "mnemonas-saf-${workerSequence.incrementAndGet()}",
            ).apply {
                isDaemon = true
            }
        },
        ThreadPoolExecutor.AbortPolicy(),
    )
    private val importOperations = ConcurrentHashMap<String, CopyOperation>()
    private val exportOperations = ConcurrentHashMap<String, CopyOperation>()
    private val pendingExportTargets = ConcurrentHashMap.newKeySet<String>()

    private lateinit var exportChannel: MethodChannel
    private lateinit var importChannel: MethodChannel
    private var pendingCreateResult: MethodChannel.Result? = null
    private var pendingOpenResult: MethodChannel.Result? = null
    private var pendingMetadataOperation: MetadataOperation? = null
    @Volatile private var destroyed = false

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        exportChannel = MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            EXPORT_CHANNEL,
        )
        exportChannel.setMethodCallHandler(::handleFileExportCall)
        importChannel = MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            IMPORT_CHANNEL,
        )
        importChannel.setMethodCallHandler(::handleFileImportCall)
    }

    private fun handleFileExportCall(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "createDocument" -> createDocument(call, result)
            "copyToDocument" -> copyToDocument(call, result)
            "cancelExport" -> cancelOperation(
                call,
                result,
                exportOperations,
                "EXPORT_CANCELLED",
                "The export was cancelled.",
            )
            else -> result.notImplemented()
        }
    }

    private fun handleFileImportCall(call: MethodCall, result: MethodChannel.Result) {
        when (call.method) {
            "pickDocuments" -> pickDocuments(result)
            "copyDocumentToFile" -> copyDocumentToFile(call, result)
            "cancelCopy" -> cancelOperation(
                call,
                result,
                importOperations,
                "IMPORT_CANCELLED",
                "The import copy was cancelled.",
            )
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
            addFlags(Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
        }
        pendingCreateResult = result
        try {
            startActivityForResult(intent, CREATE_DOCUMENT_REQUEST)
        } catch (_: Exception) {
            pendingCreateResult = null
            result.error(
                "SAVE_DIALOG_UNAVAILABLE",
                "The save dialog is unavailable.",
                null,
            )
        }
    }

    private fun pickDocuments(result: MethodChannel.Result) {
        if (pendingOpenResult != null) {
            result.error("IMPORT_IN_PROGRESS", "A file picker is already open.", null)
            return
        }
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
            putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        pendingOpenResult = result
        try {
            startActivityForResult(intent, OPEN_DOCUMENTS_REQUEST)
        } catch (_: Exception) {
            pendingOpenResult = null
            result.error("FILE_PICKER_UNAVAILABLE", "The file picker is unavailable.", null)
        }
    }

    private fun copyDocumentToFile(call: MethodCall, result: MethodChannel.Result) {
        val operationId = parseOperationId(call.argument<String>("operationId"))
        if (operationId == null) {
            result.error("INVALID_OPERATION_ID", "A valid operation ID is required.", null)
            return
        }
        val source = parseContentUri(call.argument<String>("uri"))
        if (source == null) {
            result.error("INVALID_IMPORT_SOURCE", "A content URI is required.", null)
            return
        }
        val destination = resolvePrivateDestination(
            call.argument<String>("destinationPath"),
        )
        if (destination == null) {
            result.error(
                "INVALID_IMPORT_DESTINATION",
                "An unused app-private destination is required.",
                null,
            )
            return
        }
        val parsedExpectedLength = parseOptionalLength(
            call.argument<Any>("expectedLength"),
        )
        if (parsedExpectedLength === INVALID_LENGTH) {
            result.error(
                "INVALID_IMPORT_LENGTH",
                "Expected length must be a non-negative integer.",
                null,
            )
            return
        }
        val expectedLength = parsedExpectedLength as Long?
        val parsedMaxBytes = parseOptionalLength(call.argument<Any>("maxBytes"))
        if (parsedMaxBytes === INVALID_LENGTH ||
            parsedMaxBytes == null ||
            (parsedMaxBytes as Long) <= 0
        ) {
            result.error(
                "INVALID_IMPORT_LIMIT",
                "A positive maximum import length is required.",
                null,
            )
            return
        }
        val maxBytes = parsedMaxBytes
        if (expectedLength != null && expectedLength > maxBytes) {
            result.error(
                "IMPORT_TOO_LARGE",
                "The selected document exceeds the upload size limit.",
                null,
            )
            return
        }

        val operation = CopyOperation(
            id = operationId,
            result = result,
            totalBytes = expectedLength,
        )
        submitCopyOperation(
            operation = operation,
            operations = importOperations,
            channel = importChannel,
            duplicateCode = "IMPORT_ALREADY_RUNNING",
            queueCode = "IMPORT_QUEUE_FULL",
            failureCode = "IMPORT_COPY_FAILED",
            failureMessage = "The selected document could not be copied.",
            cancelledCode = "IMPORT_CANCELLED",
            cancelledMessage = "The import copy was cancelled.",
        ) {
            copyDocumentToPrivateFile(
                source = source,
                destination = destination,
                expectedLength = expectedLength,
                maxBytes = maxBytes,
                operation = operation,
                channel = importChannel,
                operations = importOperations,
            )
        }
    }

    private fun copyToDocument(call: MethodCall, result: MethodChannel.Result) {
        val operationId = parseOperationId(call.argument<String>("operationId"))
        if (operationId == null) {
            result.error("INVALID_OPERATION_ID", "A valid operation ID is required.", null)
            return
        }
        val sourcePath = call.argument<String>("sourcePath")
        val target = parseContentUri(call.argument<String>("targetUri"))
        if (target == null) {
            result.error(
                "INVALID_EXPORT",
                "An app-private source and content URI are required.",
                null,
            )
            return
        }
        if (!pendingExportTargets.remove(target.toString())) {
            result.error(
                "INVALID_EXPORT_TARGET",
                "The export target was not created by the current save dialog.",
                null,
            )
            return
        }
        if (sourcePath.isNullOrBlank()) {
            deleteDocumentBestEffort(target)
            result.error(
                "INVALID_EXPORT",
                "An app-private source and content URI are required.",
                null,
            )
            return
        }

        val source = try {
            File(sourcePath).canonicalFile
        } catch (_: Exception) {
            deleteDocumentBestEffort(target)
            result.error(
                "INVALID_EXPORT_SOURCE",
                "The export source is invalid.",
                null,
            )
            return
        }
        val validSource = try {
            source.isFile && isAppPrivateFile(source)
        } catch (_: Exception) {
            false
        }
        if (!validSource) {
            deleteDocumentBestEffort(target)
            result.error(
                "INVALID_EXPORT_SOURCE",
                "Only app-private regular files can be exported.",
                null,
            )
            return
        }
        val sourceLength = try {
            source.length()
        } catch (_: Exception) {
            deleteDocumentBestEffort(target)
            result.error(
                "INVALID_EXPORT_SOURCE",
                "The export source length is unavailable.",
                null,
            )
            return
        }

        val operation = CopyOperation(
            id = operationId,
            result = result,
            totalBytes = sourceLength,
        )
        operation.exportTarget = target
        submitCopyOperation(
            operation = operation,
            operations = exportOperations,
            channel = exportChannel,
            duplicateCode = "EXPORT_ALREADY_RUNNING",
            queueCode = "EXPORT_QUEUE_FULL",
            failureCode = "EXPORT_FAILED",
            failureMessage = "The staged file could not be written to the destination.",
            cancelledCode = "EXPORT_CANCELLED",
            cancelledMessage = "The export was cancelled.",
        ) {
            copyPrivateFileToDocument(
                source = source,
                expectedLength = sourceLength,
                target = target,
                operation = operation,
                channel = exportChannel,
                operations = exportOperations,
            )
        }
    }

    private fun submitCopyOperation(
        operation: CopyOperation,
        operations: ConcurrentHashMap<String, CopyOperation>,
        channel: MethodChannel,
        duplicateCode: String,
        queueCode: String,
        failureCode: String,
        failureMessage: String,
        cancelledCode: String,
        cancelledMessage: String,
        copy: () -> Unit,
    ) {
        if (operations.putIfAbsent(operation.id, operation) != null) {
            discardExportTarget(operation)
            operation.result.error(
                duplicateCode,
                "An operation with this ID is already running.",
                null,
            )
            return
        }

        try {
            operation.future = ioExecutor.submit {
                try {
                    operation.checkCancelled()
                    postProgress(
                        channel,
                        operations,
                        operation,
                        transferred = 0,
                        force = true,
                    )
                    copy()
                    operation.checkCancelled()
                    completeOperation(
                        operations,
                        operation,
                        success = true,
                        failureCode = failureCode,
                        failureMessage = failureMessage,
                        cancelledCode = cancelledCode,
                        cancelledMessage = cancelledMessage,
                    )
                } catch (_: OperationCancelledException) {
                    completeOperation(
                        operations,
                        operation,
                        success = false,
                        failureCode = cancelledCode,
                        failureMessage = cancelledMessage,
                        cancelledCode = cancelledCode,
                        cancelledMessage = cancelledMessage,
                    )
                } catch (error: OperationFailureException) {
                    completeOperation(
                        operations,
                        operation,
                        success = false,
                        failureCode = error.code,
                        failureMessage = error.publicMessage,
                        cancelledCode = cancelledCode,
                        cancelledMessage = cancelledMessage,
                    )
                } catch (_: Exception) {
                    val cancelled = operation.cancelled.get()
                    completeOperation(
                        operations,
                        operation,
                        success = false,
                        failureCode = if (cancelled) cancelledCode else failureCode,
                        failureMessage = if (cancelled) {
                            cancelledMessage
                        } else {
                            failureMessage
                        },
                        cancelledCode = cancelledCode,
                        cancelledMessage = cancelledMessage,
                    )
                } finally {
                    operation.clearResources()
                }
            }
        } catch (_: RejectedExecutionException) {
            operations.remove(operation.id, operation)
            discardExportTarget(operation)
            operation.completed.set(true)
            operation.result.error(
                queueCode,
                "Too many storage operations are waiting.",
                null,
            )
        }
    }

    private fun cancelOperation(
        call: MethodCall,
        result: MethodChannel.Result,
        operations: ConcurrentHashMap<String, CopyOperation>,
        cancelledCode: String,
        cancelledMessage: String,
    ) {
        val operationId = parseOperationId(call.argument<String>("operationId"))
        if (operationId == null) {
            result.error("INVALID_OPERATION_ID", "A valid operation ID is required.", null)
            return
        }
        val operation = operations[operationId]
        if (operation != null) {
            if (operation.cancel()) {
                completeOperation(
                    operations,
                    operation,
                    success = false,
                    failureCode = cancelledCode,
                    failureMessage = cancelledMessage,
                    cancelledCode = cancelledCode,
                    cancelledMessage = cancelledMessage,
                )
            }
        }
        result.success(null)
    }

    private fun completeOperation(
        operations: ConcurrentHashMap<String, CopyOperation>,
        operation: CopyOperation,
        success: Boolean,
        failureCode: String,
        failureMessage: String,
        cancelledCode: String,
        cancelledMessage: String,
    ) {
        mainHandler.post {
            if (!operation.completed.compareAndSet(false, true)) {
                return@post
            }
            operations.remove(operation.id, operation)
            if (operation.exportCommitted.get()) {
                operation.acceptExportCommit()
                operation.result.success(null)
                return@post
            }
            if (destroyed || operation.cancelled.get()) {
                operation.cancel()
                discardExportTarget(operation)
                operation.result.error(cancelledCode, cancelledMessage, null)
                return@post
            }
            if (success) {
                operation.acceptImportCommit()
                operation.acceptExportCommit()
                operation.result.success(null)
            } else {
                discardExportTarget(operation)
                operation.result.error(failureCode, failureMessage, null)
            }
        }
    }

    private fun postProgress(
        channel: MethodChannel,
        operations: ConcurrentHashMap<String, CopyOperation>,
        operation: CopyOperation,
        transferred: Long,
        force: Boolean = false,
    ) {
        if (!operation.shouldEmitProgress(transferred, force)) {
            return
        }
        mainHandler.post {
            if (destroyed ||
                operation.cancelled.get() ||
                operation.completed.get() ||
                operations[operation.id] !== operation
            ) {
                return@post
            }
            channel.invokeMethod(
                "copyProgress",
                mapOf(
                    "operationId" to operation.id,
                    "transferred" to transferred,
                    "total" to operation.totalBytes,
                ),
            )
        }
    }

    private fun isAppPrivateFile(file: File): Boolean {
        val allowedRoots = listOf(cacheDir.canonicalFile, filesDir.canonicalFile)
        return allowedRoots.any { root ->
            file.path.startsWith(root.path + File.separator)
        }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        when (requestCode) {
            CREATE_DOCUMENT_REQUEST -> finishCreateDocument(resultCode, data)
            OPEN_DOCUMENTS_REQUEST -> finishOpenDocuments(resultCode, data)
            else -> super.onActivityResult(requestCode, resultCode, data)
        }
    }

    private fun finishCreateDocument(resultCode: Int, data: Intent?) {
        val result = pendingCreateResult ?: return
        if (resultCode == Activity.RESULT_CANCELED) {
            pendingCreateResult = null
            result.success(null)
            return
        }
        val uri = data?.data?.let { parseContentUri(it.toString()) }
        if (resultCode != Activity.RESULT_OK ||
            uri == null ||
            data.flags and Intent.FLAG_GRANT_WRITE_URI_PERMISSION == 0
        ) {
            pendingCreateResult = null
            result.error("SAVE_DIALOG_FAILED", "No writable destination was returned.", null)
            return
        }

        pendingCreateResult = null
        pendingExportTargets.add(uri.toString())
        result.success(uri.toString())
    }

    private fun finishOpenDocuments(resultCode: Int, data: Intent?) {
        val result = pendingOpenResult ?: return
        if (resultCode == Activity.RESULT_CANCELED) {
            pendingOpenResult = null
            result.success(emptyList<Map<String, Any?>>())
            return
        }
        if (resultCode != Activity.RESULT_OK || data == null) {
            pendingOpenResult = null
            result.error("FILE_PICKER_FAILED", "No readable documents were returned.", null)
            return
        }
        if (data.flags and Intent.FLAG_GRANT_READ_URI_PERMISSION == 0) {
            pendingOpenResult = null
            result.error(
                "FILE_PICKER_FAILED",
                "No readable documents were returned.",
                null,
            )
            return
        }

        val uris = linkedSetOf<Uri>()
        data.clipData?.let { clipData ->
            if (clipData.itemCount > MAX_SELECTED_DOCUMENTS) {
                pendingOpenResult = null
                result.error(
                    "TOO_MANY_IMPORTS",
                    "Select no more than $MAX_SELECTED_DOCUMENTS documents at once.",
                    null,
                )
                return
            }
            for (index in 0 until clipData.itemCount) {
                clipData.getItemAt(index).uri?.let(uris::add)
            }
        }
        data.data?.let(uris::add)
        if (uris.isEmpty() || uris.size > MAX_SELECTED_DOCUMENTS) {
            pendingOpenResult = null
            result.error(
                "FILE_PICKER_FAILED",
                "No readable documents were returned.",
                null,
            )
            return
        }
        if (uris.any { parseContentUri(it.toString()) == null }) {
            pendingOpenResult = null
            result.error(
                "FILE_PICKER_FAILED",
                "Only content documents can be imported.",
                null,
            )
            return
        }

        val operation = MetadataOperation(result)
        pendingMetadataOperation = operation
        try {
            operation.future = ioExecutor.submit {
                try {
                    val values = uris.map { uri ->
                        operation.checkCancelled()
                        readDocumentMetadata(uri, operation.cancellationSignal)
                    }
                    operation.checkCancelled()
                    mainHandler.post {
                        if (destroyed ||
                            operation.cancelled.get() ||
                            pendingMetadataOperation !== operation
                        ) {
                            return@post
                        }
                        pendingMetadataOperation = null
                        pendingOpenResult = null
                        result.success(values)
                    }
                } catch (_: Exception) {
                    mainHandler.post {
                        if (pendingMetadataOperation !== operation) {
                            return@post
                        }
                        pendingMetadataOperation = null
                        pendingOpenResult = null
                        if (!destroyed) {
                            result.error(
                                if (operation.cancelled.get()) {
                                    "IMPORT_METADATA_CANCELLED"
                                } else {
                                    "IMPORT_METADATA_FAILED"
                                },
                                if (operation.cancelled.get()) {
                                    "Document metadata loading was cancelled."
                                } else {
                                    "Metadata for the selected documents could not be read."
                                },
                                null,
                            )
                        }
                    }
                }
            }
        } catch (_: RejectedExecutionException) {
            pendingMetadataOperation = null
            pendingOpenResult = null
            result.error(
                "IMPORT_QUEUE_FULL",
                "Too many storage operations are waiting.",
                null,
            )
        }
    }

    override fun onDestroy() {
        destroyed = true
        pendingCreateResult?.error(
            "SAVE_DIALOG_CLOSED",
            "The save dialog closed before returning a destination.",
            null,
        )
        pendingCreateResult = null
        pendingMetadataOperation?.cancel()
        pendingMetadataOperation = null
        pendingOpenResult?.error(
            "FILE_PICKER_CLOSED",
            "The file picker closed before returning documents.",
            null,
        )
        pendingOpenResult = null
        cancelOperationsForDestroy(
            importOperations,
            "IMPORT_CANCELLED",
            "The import copy stopped because the activity was destroyed.",
        )
        cancelOperationsForDestroy(
            exportOperations,
            "EXPORT_CANCELLED",
            "The export stopped because the activity was destroyed.",
        )
        pendingExportTargets.toList().forEach { value ->
            parseContentUri(value)?.let(::deleteDocumentBestEffort)
        }
        pendingExportTargets.clear()
        ioExecutor.shutdownNow()
        super.onDestroy()
    }

    private fun cancelOperationsForDestroy(
        operations: ConcurrentHashMap<String, CopyOperation>,
        code: String,
        message: String,
    ) {
        operations.values.toList().forEach { operation ->
            val cancelled = operation.cancel()
            if (operation.completed.compareAndSet(false, true)) {
                if (!cancelled && operation.exportCommitted.get()) {
                    operation.acceptExportCommit()
                    operation.result.success(null)
                } else {
                    discardExportTarget(operation)
                    operation.result.error(code, message, null)
                }
            }
        }
        operations.clear()
    }

    private fun readDocumentMetadata(
        uri: Uri,
        cancellationSignal: CancellationSignal,
    ): Map<String, Any?> {
        var displayName: String? = null
        var size: Long? = null
        contentResolver.query(
            uri,
            arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE),
            null,
            null,
            null,
            cancellationSignal,
        )?.use { cursor ->
            if (cursor.moveToFirst()) {
                val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (nameIndex >= 0 && !cursor.isNull(nameIndex)) {
                    displayName = cursor.getString(nameIndex)
                }
                val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
                if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) {
                    cursor.getLong(sizeIndex).takeIf { it >= 0 }?.let { size = it }
                }
            }
        }

        return mapOf(
            "uri" to uri.toString(),
            "displayName" to sanitizeImportFileName(displayName ?: uri.lastPathSegment),
            "mimeType" to sanitizeOptionalMimeType(contentResolver.getType(uri)),
            "size" to size,
        )
    }

    private fun copyDocumentToPrivateFile(
        source: Uri,
        destination: File,
        expectedLength: Long?,
        maxBytes: Long,
        operation: CopyOperation,
        channel: MethodChannel,
        operations: ConcurrentHashMap<String, CopyOperation>,
    ) {
        operation.checkCancelled()
        val asset = contentResolver.openAssetFileDescriptor(
            source,
            "r",
            operation.cancellationSignal,
        ) ?: throw IllegalStateException("The selected document is unavailable.")
        operation.register(asset)
        val input = operation.register(asset.createInputStream())
        val temporary = File(
            destination.parentFile,
            ".${destination.name}.mnemonas-${UUID.randomUUID()}.part",
        )
        if (!temporary.createNewFile()) {
            throw IllegalStateException("A temporary import file could not be created.")
        }

        var renamed = false
        try {
            input.use { stream ->
                val output = operation.register(FileOutputStream(temporary))
                output.use {
                    val buffer = ByteArray(COPY_BUFFER_SIZE)
                    var copied = 0L
                    while (true) {
                        operation.checkCancelled()
                        val count = stream.read(buffer)
                        if (count < 0) {
                            break
                        }
                        if (count == 0) {
                            continue
                        }
                        if (count.toLong() > maxBytes - copied) {
                            throw OperationFailureException(
                                code = "IMPORT_TOO_LARGE",
                                publicMessage =
                                    "The selected document exceeds the upload size limit.",
                            )
                        }
                        if (expectedLength != null &&
                            count.toLong() > expectedLength - copied
                        ) {
                            throw IllegalStateException(
                                "The selected document exceeded its expected length.",
                            )
                        }
                        output.write(buffer, 0, count)
                        copied += count.toLong()
                        postProgress(channel, operations, operation, copied)
                    }
                    if (expectedLength != null && copied != expectedLength) {
                        throw IllegalStateException("The selected document length changed.")
                    }
                    operation.checkCancelled()
                    output.flush()
                    output.fd.sync()
                    operation.checkCancelled()
                    postProgress(
                        channel,
                        operations,
                        operation,
                        copied,
                        force = true,
                    )
                }
            }
            operation.checkCancelled()
            if (destination.exists() || !temporary.renameTo(destination)) {
                throw IllegalStateException("The imported document could not be committed.")
            }
            renamed = true
            operation.committedImportFile = destination
            operation.checkCancelled()
        } finally {
            if (!renamed && temporary.exists()) {
                temporary.delete()
            }
            if (operation.cancelled.get()) {
                operation.discardImportCommit()
            }
        }
    }

    private fun copyPrivateFileToDocument(
        source: File,
        expectedLength: Long,
        target: Uri,
        operation: CopyOperation,
        channel: MethodChannel,
        operations: ConcurrentHashMap<String, CopyOperation>,
    ) {
        operation.checkCancelled()
        val descriptor = contentResolver.openFileDescriptor(
            target,
            "rwt",
            operation.cancellationSignal,
        ) ?: throw IllegalStateException("The selected destination is unavailable.")
        operation.register(descriptor)
        val input = operation.register(FileInputStream(source))
        val output = operation.register(FileOutputStream(descriptor.fileDescriptor))
        var copied = 0L
        input.use { stream ->
            output.use {
                val buffer = ByteArray(COPY_BUFFER_SIZE)
                while (true) {
                    operation.checkCancelled()
                    val count = stream.read(buffer)
                    if (count < 0) {
                        break
                    }
                    if (count == 0) {
                        continue
                    }
                    if (count.toLong() > expectedLength - copied) {
                        throw IllegalStateException(
                            "The staged export changed while it was being copied.",
                        )
                    }
                    output.write(buffer, 0, count)
                    copied += count.toLong()
                    postProgress(channel, operations, operation, copied)
                }
                if (copied != expectedLength) {
                    throw IllegalStateException(
                        "The staged export changed while it was being copied.",
                    )
                }
                operation.checkCancelled()
                output.flush()
                output.fd.sync()
                operation.checkCancelled()
            }
        }
        descriptor.close()
        operation.checkCancelled()
        val targetLength = readDocumentSize(target, operation.cancellationSignal)
        if (targetLength != null && targetLength != copied) {
            throw IllegalStateException("The destination length does not match the export.")
        }
        operation.markExportCommit()
        postProgress(
            channel,
            operations,
            operation,
            copied,
            force = true,
        )
    }

    private fun readDocumentSize(
        uri: Uri,
        cancellationSignal: CancellationSignal,
    ): Long? {
        contentResolver.query(
            uri,
            arrayOf(OpenableColumns.SIZE),
            null,
            null,
            null,
            cancellationSignal,
        )?.use { cursor ->
            if (cursor.moveToFirst()) {
                val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
                if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) {
                    return cursor.getLong(sizeIndex).takeIf { it >= 0 }
                }
            }
        }
        return null
    }

    private fun parseContentUri(value: String?): Uri? {
        if (value.isNullOrBlank()) {
            return null
        }
        val uri = try {
            Uri.parse(value)
        } catch (_: Exception) {
            return null
        }
        return uri.takeIf {
            it.scheme == ContentResolver.SCHEME_CONTENT &&
                !it.authority.isNullOrBlank() &&
                it.fragment == null
        }
    }

    private fun parseOperationId(value: String?): String? {
        return value?.takeIf(OPERATION_ID_PATTERN::matches)
    }

    private fun discardExportTarget(operation: CopyOperation) {
        val target = operation.exportTarget
        operation.exportTarget = null
        if (target != null) {
            deleteDocumentBestEffort(target)
        }
    }

    private fun deleteDocumentBestEffort(uri: Uri) {
        try {
            DocumentsContract.deleteDocument(contentResolver, uri)
        } catch (_: Exception) {
            // Not every document provider supports deleting a failed export.
        }
    }

    private fun parseOptionalLength(value: Any?): Any? {
        val parsed = when (value) {
            null -> null
            is Int -> value.toLong()
            is Long -> value
            else -> return INVALID_LENGTH
        }
        return if (parsed == null || parsed >= 0) parsed else INVALID_LENGTH
    }

    private fun resolvePrivateDestination(value: String?): File? {
        if (value.isNullOrBlank()) {
            return null
        }
        val candidate = try {
            File(value).canonicalFile
        } catch (_: Exception) {
            return null
        }
        val initiallyPrivate = try {
            candidate.isAbsolute && !candidate.exists() && isAppPrivateFile(candidate)
        } catch (_: Exception) {
            false
        }
        if (!initiallyPrivate) {
            return null
        }
        val parent = candidate.parentFile ?: return null
        if ((!parent.exists() && !parent.mkdirs()) || !parent.isDirectory) {
            return null
        }
        return try {
            candidate.takeIf { isAppPrivateFile(it.canonicalFile) && !it.exists() }
        } catch (_: Exception) {
            null
        }
    }

    private fun sanitizeFileName(value: String?): String {
        val candidate = value
            ?.replace(Regex("[\\u0000-\\u001F\\u007F/\\\\]"), "_")
            ?.trim()
            ?.take(180)
            .orEmpty()
        return candidate.ifBlank { "MnemoNAS-download" }
    }

    private fun sanitizeImportFileName(value: String?): String {
        val candidate = sanitizeFileName(value)
        return candidate.takeUnless { it == "." || it == ".." } ?: "MnemoNAS-upload"
    }

    private fun sanitizeMimeType(value: String?): String {
        return sanitizeOptionalMimeType(value) ?: "application/octet-stream"
    }

    private fun sanitizeOptionalMimeType(value: String?): String? {
        val candidate = value?.trim().orEmpty()
        return candidate.takeIf {
            Regex("^[A-Za-z0-9!#$&^_.+-]+/[A-Za-z0-9!#$&^_.+-]+$").matches(it)
        }
    }

    private class CopyOperation(
        val id: String,
        val result: MethodChannel.Result,
        val totalBytes: Long?,
    ) {
        val cancelled = AtomicBoolean()
        val completed = AtomicBoolean()
        val exportCommitted = AtomicBoolean()
        val cancellationSignal = CancellationSignal()
        @Volatile var future: Future<*>? = null
        @Volatile var committedImportFile: File? = null
        @Volatile var exportTarget: Uri? = null

        private val resourceLock = Any()
        private val outcomeLock = Any()
        private val resources = mutableSetOf<Closeable>()
        private var lastProgress = -PROGRESS_INTERVAL_BYTES

        fun <T : Closeable> register(resource: T): T {
            synchronized(resourceLock) {
                if (cancelled.get()) {
                    try {
                        resource.close()
                    } catch (_: Exception) {
                        // Cancellation cleanup is best effort.
                    }
                    throw OperationCancelledException()
                }
                resources.add(resource)
            }
            return resource
        }

        fun checkCancelled() {
            if (cancelled.get() || Thread.currentThread().isInterrupted) {
                throw OperationCancelledException()
            }
        }

        fun cancel(): Boolean {
            synchronized(outcomeLock) {
                if (exportCommitted.get()) {
                    return false
                }
                cancelled.set(true)
            }
            cancellationSignal.cancel()
            val snapshot = synchronized(resourceLock) {
                val registered = resources.toList()
                resources.clear()
                registered
            }
            snapshot.forEach { resource ->
                try {
                    resource.close()
                } catch (_: Exception) {
                    // Cancellation cleanup is best effort.
                }
            }
            discardImportCommit()
            future?.cancel(true)
            return true
        }

        fun clearResources() {
            val snapshot = synchronized(resourceLock) {
                val registered = resources.toList()
                resources.clear()
                registered
            }
            snapshot.forEach { resource ->
                try {
                    resource.close()
                } catch (_: Exception) {
                    // Resource cleanup after a completed operation is best effort.
                }
            }
        }

        fun shouldEmitProgress(transferred: Long, force: Boolean): Boolean {
            synchronized(this) {
                if (!force && transferred - lastProgress < PROGRESS_INTERVAL_BYTES) {
                    return false
                }
                if (transferred < lastProgress) {
                    return false
                }
                lastProgress = transferred
                return true
            }
        }

        fun acceptImportCommit() {
            committedImportFile = null
        }

        fun markExportCommit() {
            synchronized(outcomeLock) {
                if (cancelled.get()) {
                    throw OperationCancelledException()
                }
                exportCommitted.set(true)
            }
        }

        fun acceptExportCommit() {
            exportTarget = null
        }

        fun discardImportCommit() {
            val file = committedImportFile
            committedImportFile = null
            if (file != null && file.exists()) {
                file.delete()
            }
        }
    }

    private class MetadataOperation(
        val result: MethodChannel.Result,
    ) {
        val cancelled = AtomicBoolean()
        val cancellationSignal = CancellationSignal()
        @Volatile var future: Future<*>? = null

        fun checkCancelled() {
            if (cancelled.get() || Thread.currentThread().isInterrupted) {
                throw OperationCancelledException()
            }
        }

        fun cancel() {
            cancelled.set(true)
            cancellationSignal.cancel()
            future?.cancel(true)
        }
    }

    private class OperationCancelledException : Exception()

    private class OperationFailureException(
        val code: String,
        val publicMessage: String,
    ) : Exception()
}

private val INVALID_LENGTH = Any()
