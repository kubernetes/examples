#!/usr/bin/env python3
"""
PyTorch Distributed Data Parallel (DDP) Training Script

This script demonstrates distributed training using PyTorch DDP.
It trains a simple CNN on CIFAR-10 as an example.
"""

import argparse
import os
import torch
import torch.distributed as dist
import torch.nn as nn
import torch.nn.functional as F
import torch.optim as optim
from torch.nn.parallel import DistributedDataParallel as DDP
from torch.utils.data import DataLoader
from torch.utils.data.distributed import DistributedSampler
from torchvision import datasets, transforms
from torch.utils.tensorboard import SummaryWriter
import time


class SimpleCNN(nn.Module):
    """Simple CNN for CIFAR-10 classification"""
    def __init__(self):
        super(SimpleCNN, self).__init__()
        self.conv1 = nn.Conv2d(3, 32, 3, padding=1)
        self.conv2 = nn.Conv2d(32, 64, 3, padding=1)
        self.conv3 = nn.Conv2d(64, 128, 3, padding=1)
        self.pool = nn.MaxPool2d(2, 2)
        self.fc1 = nn.Linear(128 * 4 * 4, 512)
        self.fc2 = nn.Linear(512, 10)
        self.dropout = nn.Dropout(0.5)

    def forward(self, x):
        x = self.pool(F.relu(self.conv1(x)))
        x = self.pool(F.relu(self.conv2(x)))
        x = self.pool(F.relu(self.conv3(x)))
        x = x.view(-1, 128 * 4 * 4)
        x = F.relu(self.fc1(x))
        x = self.dropout(x)
        x = self.fc2(x)
        return x


def setup(rank, world_size, master_addr, master_port):
    """Initialize the process group for distributed training"""
    os.environ['MASTER_ADDR'] = master_addr
    os.environ['MASTER_PORT'] = str(master_port)
    
    # Initialize the process group
    dist.init_process_group("nccl", rank=rank, world_size=world_size)
    torch.cuda.set_device(rank % torch.cuda.device_count())


def cleanup():
    """Clean up the process group"""
    dist.destroy_process_group()


def train(rank, world_size, args):
    """Main training function"""
    print(f"Running DDP training on rank {rank} of {world_size}")
    
    # Setup distributed training
    setup(rank, world_size, args.master_addr, args.master_port)
    
    # Create model and move it to GPU
    model = SimpleCNN().to(rank)
    ddp_model = DDP(model, device_ids=[rank % torch.cuda.device_count()])
    
    # Loss and optimizer
    criterion = nn.CrossEntropyLoss()
    optimizer = optim.Adam(ddp_model.parameters(), lr=0.001)
    
    # Prepare data
    transform = transforms.Compose([
        transforms.ToTensor(),
        transforms.Normalize((0.5, 0.5, 0.5), (0.5, 0.5, 0.5))
    ])
    
    # Use DistributedSampler to ensure each process gets a different subset
    # Prevent race conditions by only downloading on rank 0
    if rank == 0:
        datasets.CIFAR10(root=args.data_dir, train=True, download=True, transform=transform)
    dist.barrier() # Wait for rank 0 to finish download
    dataset = datasets.CIFAR10(root=args.data_dir, train=True, download=False, transform=transform)

    sampler = DistributedSampler(dataset, num_replicas=world_size, rank=rank)
    dataloader = DataLoader(
        dataset,
        batch_size=args.batch_size,
        sampler=sampler,
        num_workers=2
    )
    
    # TensorBoard writer (only on rank 0)
    if rank == 0:
        writer = SummaryWriter(log_dir=os.path.join(args.output_dir, 'tensorboard'))
    
    # Training loop
    ddp_model.train()
    for epoch in range(args.num_epochs):
        sampler.set_epoch(epoch)  # Important for shuffling
        epoch_loss = 0.0
        num_batches = 0
        
        for batch_idx, (data, target) in enumerate(dataloader):
            data, target = data.to(rank), target.to(rank)
            
            optimizer.zero_grad()
            output = ddp_model(data)
            loss = criterion(output, target)
            loss.backward()
            optimizer.step()
            
            epoch_loss += loss.item()
            num_batches += 1
            
            if batch_idx % 100 == 0 and rank == 0:
                print(f'Epoch {epoch}, Batch {batch_idx}, Loss: {loss.item():.4f}')
        
        avg_loss = epoch_loss / num_batches if num_batches > 0 else 0
        
        if rank == 0:
            writer.add_scalar('Loss/Train', avg_loss, epoch)
            print(f'Epoch {epoch} completed. Average Loss: {avg_loss:.4f}')
            
            # Save checkpoint
            # Save checkpoints only from rank 0 to avoid file corruption
            if rank == 0:
                checkpoint_path = os.path.join(args.output_dir, f'checkpoint_epoch_{epoch}.pt')
            torch.save({
                'epoch': epoch,
                'model_state_dict': ddp_model.module.state_dict(),
                'optimizer_state_dict': optimizer.state_dict(),
                'loss': avg_loss,
            }, checkpoint_path)
            print(f'Checkpoint saved to {checkpoint_path}')
    
    if rank == 0:
        writer.close()
        # Save final model only from rank 0
        if rank == 0:
            final_model_path = os.path.join(args.output_dir, 'final_model.pt')
        torch.save(ddp_model.module.state_dict(), final_model_path)
        print(f'Final model saved to {final_model_path}')
    
    cleanup()


def main():
    parser = argparse.ArgumentParser(description='PyTorch DDP Training')
    parser.add_argument('--data-dir', type=str, default='/data',
                        help='Directory for training data')
    parser.add_argument('--output-dir', type=str, default='/output',
                        help='Directory for outputs and checkpoints')
    parser.add_argument('--num-epochs', type=int, default=10,
                        help='Number of training epochs')
    parser.add_argument('--batch-size', type=int, default=32,
                        help='Batch size per GPU')
    parser.add_argument('--world-size', type=int, required=True,
                        help='Total number of processes')
    parser.add_argument('--rank', type=int, required=True,
                        help='Rank of this process')
    parser.add_argument('--master-addr', type=str, required=True,
                        help='Address of the master node')
    parser.add_argument('--master-port', type=int, default=29500,
                        help='Port for distributed communication')
    
    args = parser.parse_args()
    
    # Create output directory if it doesn't exist
    os.makedirs(args.output_dir, exist_ok=True)
    
    # Get local rank (which GPU this process should use)
    local_rank = args.rank % torch.cuda.device_count()
    torch.cuda.set_device(local_rank)
    
    train(args.rank, args.world_size, args)


if __name__ == '__main__':
    main()

