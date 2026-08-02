use bytes::Bytes;
use futures_util::Stream;
use std::{
    io,
    pin::Pin,
    sync::{LazyLock, Mutex},
    task::{Context, Poll},
};
use tokio::io::{AsyncRead, ReadBuf};

const MAX_CACHED_BUFFERS_PER_CLASS: usize = 8;

pub struct PooledReaderStream<R, G = ()> {
    reader: R,
    buffer: Option<Vec<u8>>,
    chunk_size: usize,
    done: bool,
    _guard: G,
}

pub struct SizeLimitedStream<S> {
    inner: S,
    maximum: u64,
    seen: u64,
}

impl<S> SizeLimitedStream<S> {
    pub fn new(inner: S, maximum: u64) -> Self {
        Self {
            inner,
            maximum,
            seen: 0,
        }
    }
}

impl<S: Stream<Item = io::Result<Bytes>> + Unpin> Stream for SizeLimitedStream<S> {
    type Item = io::Result<Bytes>;

    fn poll_next(mut self: Pin<&mut Self>, context: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        match Pin::new(&mut self.inner).poll_next(context) {
            Poll::Ready(Some(Ok(chunk))) => {
                let length = u64::try_from(chunk.len()).unwrap_or(u64::MAX);
                self.seen = self.seen.saturating_add(length);
                if self.seen > self.maximum {
                    Poll::Ready(Some(Err(io::Error::new(
                        io::ErrorKind::InvalidData,
                        "object payload exceeds the platformd size limit",
                    ))))
                } else {
                    Poll::Ready(Some(Ok(chunk)))
                }
            }
            other => other,
        }
    }
}

impl<R> PooledReaderStream<R, ()> {
    pub fn new(reader: R, chunk_size: usize) -> Self {
        Self::with_guard(reader, chunk_size, ())
    }
}

impl<R, G> PooledReaderStream<R, G> {
    pub fn with_guard(reader: R, chunk_size: usize, guard: G) -> Self {
        Self {
            reader,
            buffer: Some(pool().take(chunk_size)),
            chunk_size,
            done: false,
            _guard: guard,
        }
    }
}

impl<R: AsyncRead + Unpin, G: Unpin> Stream for PooledReaderStream<R, G> {
    type Item = io::Result<Bytes>;

    fn poll_next(mut self: Pin<&mut Self>, context: &mut Context<'_>) -> Poll<Option<Self::Item>> {
        if self.done {
            return Poll::Ready(None);
        }
        let mut buffer = self
            .buffer
            .take()
            .unwrap_or_else(|| pool().take(self.chunk_size));
        buffer.clear();

        let read_result = {
            let spare = &mut buffer.spare_capacity_mut()[..self.chunk_size];
            let mut read_buffer = ReadBuf::uninit(spare);
            match Pin::new(&mut self.reader).poll_read(context, &mut read_buffer) {
                Poll::Pending => {
                    self.buffer = Some(buffer);
                    return Poll::Pending;
                }
                Poll::Ready(result) => (result, read_buffer.filled().len()),
            }
        };

        match read_result {
            (Err(error), _) => {
                pool().release(self.chunk_size, buffer);
                self.done = true;
                Poll::Ready(Some(Err(error)))
            }
            (Ok(()), 0) => {
                pool().release(self.chunk_size, buffer);
                self.done = true;
                Poll::Ready(None)
            }
            (Ok(()), filled) => {
                // AsyncRead initialized exactly `filled` bytes in the spare capacity.
                // Exposing only that prefix preserves Vec's initialization invariant.
                unsafe { buffer.set_len(filled) };
                Poll::Ready(Some(Ok(Bytes::from_owner(PooledBuffer {
                    buffer: Some(buffer),
                    chunk_size: self.chunk_size,
                }))))
            }
        }
    }
}

struct PooledBuffer {
    buffer: Option<Vec<u8>>,
    chunk_size: usize,
}

impl AsRef<[u8]> for PooledBuffer {
    fn as_ref(&self) -> &[u8] {
        self.buffer.as_deref().unwrap_or_default()
    }
}

impl Drop for PooledBuffer {
    fn drop(&mut self) {
        if let Some(buffer) = self.buffer.take() {
            pool().release(self.chunk_size, buffer);
        }
    }
}

#[derive(Default)]
struct BufferPool {
    classes: Mutex<Vec<(usize, Vec<Vec<u8>>)>>,
}

impl BufferPool {
    fn take(&self, size: usize) -> Vec<u8> {
        let mut classes = self.classes.lock().expect("stream buffer pool poisoned");
        if let Some((_, buffers)) = classes.iter_mut().find(|(class, _)| *class == size)
            && let Some(buffer) = buffers.pop()
        {
            return buffer;
        }
        Vec::with_capacity(size)
    }

    fn release(&self, size: usize, mut buffer: Vec<u8>) {
        if buffer.capacity() < size {
            return;
        }
        buffer.clear();
        let mut classes = self.classes.lock().expect("stream buffer pool poisoned");
        let buffers =
            if let Some((_, buffers)) = classes.iter_mut().find(|(class, _)| *class == size) {
                buffers
            } else {
                classes.push((size, Vec::new()));
                &mut classes.last_mut().expect("buffer class was inserted").1
            };
        if buffers.len() < MAX_CACHED_BUFFERS_PER_CLASS {
            buffers.push(buffer);
        }
    }
}

fn pool() -> &'static BufferPool {
    static POOL: LazyLock<BufferPool> = LazyLock::new(BufferPool::default);
    &POOL
}
